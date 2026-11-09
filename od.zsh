#!/usr/bin/env zsh

LOG_FILE="${1:-/var/log/ollama.log}"
API_URL="http://127.0.0.1:11434"
SHM_DIR="/dev/shm/skynet_$$"

# Tworzymy tymczasowy katalog w RAMie dla IPC (Inter-Process Communication)
mkdir -p "$SHM_DIR"

# Ukrycie kursora i czyszczenie procesów potomnych przy wyjściu
print -n '\e[?25l'
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$SHM_DIR"; print -n "\e[?25h"; clear; exit' INT TERM EXIT

# ==============================================================================
# WORKER 1: API POLLER (OLLAMA STATE)
# ==============================================================================
(
    while true; do
        res=$(curl -s --max-time 1 "$API_URL/api/ps")
        if [[ -n "$res" ]]; then
            # Parowanie JSONa do płaskiego pliku dla AWK (Model | Size GB | VRAM GB | Quant)
            echo "$res" | jq -r 'if .models | length > 0 then .models[0] | "\(.name)|\(.size/1073741824)|\(.size_vram/1073741824)|\(.details.quantization_level)" else "Brak_Modelu|0|0|N/A" end' > "$SHM_DIR/api.txt"
        else
            echo "API_OFFLINE|0|0|N/A" > "$SHM_DIR/api.txt"
        fi
        sleep 1
    done
) &

# ==============================================================================
# WORKER 2: HARDWARE POLLER (AMD ROCm)
# ==============================================================================
(
    while true; do
        # Uderzamy prosto w sterownik. Zakładamy GPU0. (Temp Edge | GPU Load % | VRAM % lub MB)
        temp=$(rocm-smi --showtemp -q 2>/dev/null | grep -oP 'Temperature.*: \K[0-9.]+' | head -1 || echo "0")
        load=$(rocm-smi --showuse -q 2>/dev/null | grep -oP 'GPU use.*: \K[0-9]+' || echo "0")
        vram=$(rocm-smi --showmemuse -q 2>/dev/null | grep -oP 'VRAM Usage.*: \K[0-9]+' || echo "0")
        echo "${temp}|${load}|${vram}" > "$SHM_DIR/rocm.txt"
        sleep 1
    done
) &

# ==============================================================================
# MAIN ENGINE: GAWK FIREHOSE & RENDERER
# ==============================================================================
clear
tail -F "$LOG_FILE" 2>/dev/null | gawk -v shm="$SHM_DIR" '
BEGIN {
    uptime_start = systime()

    # Init domyślnych wartości
    api_model="Waiting..."; api_size="0.00"; api_vram="0.00"; api_quant="N/A"
    gpu_temp="0"; gpu_load="0"; gpu_vram="0"

    prog="0%"; tps="0.00"; n_gen="0"; tg="0.00"; tg3s="0.00"; ctx_size="0.00 MiB"
    temp="N/A"; top_k="N/A"; top_p="N/A"; rep_pen="N/A"; ctx_slot="N/A"
    prompt_eval="0.00 ms"; eval_time="0.00 ms"; total_time="0.00 ms"
    http_code="---"; http_method="NONE"; http_path="---"; http_time="0s"

    # Ring buffer na logi (ostatnie 5 akcji)
    for(i=1; i<=5; i++) event_buf[i] = ""
    last_render = systime()
}

function push_event(msg) {
    ts = strftime("%H:%M:%S", systime())
    formatted = "\033[38;5;245m[" ts "]\033[0m " msg
    for (i = 5; i > 1; i--) event_buf[i] = event_buf[i-1]
    event_buf[1] = formatted
}

{
    raw = $0

    # Środowisko i Daemon
    if (match(raw, /msg="server listening"/)) push_event("\033[1;32mOllama Daemon wstał na nasłuchu\033[0m")
    if (match(raw, /msg="loading model" .*model="?([^" ]+)"?/, a)) push_event("\033[1;34mŁadowanie struktury modelu:\033[0m " a[1])

    # Parametry Inicjalizacyjne
    if (match(raw, /temp = ([0-9.]+)/, a)) temp = a[1]
    if (match(raw, /top_k = ([0-9]+)/, a)) top_k = a[1]
    if (match(raw, /top_p = ([0-9.]+)/, a)) top_p = a[1]
    if (match(raw, /repeat_penalty = ([0-9.]+)/, a)) rep_pen = a[1]
    if (match(raw, /n_ctx_slot = ([0-9]+)/, a)) ctx_slot = a[1]

    # Processing (Mielenie Promptu / Context Checkpoints)
    if (match(raw, /progress = ([0-9.]+)/, a)) {
        prog = sprintf("%d%%", a[1] * 100)
        if (match(raw, /([0-9.]+)\s*tokens per second/, b)) tps = b[1]
    }
    else if (match(raw, /created context checkpoint.*size = ([0-9.]+ MiB)/, a)) {
        ctx_size = a[1]
        push_event("\033[38;5;135mAlokacja Checkpointu K/V Cache:\033[0m " a[1])
    }
    else if (match(raw, /restored context checkpoint/, a)) {
        push_event("\033[38;5;118mCache Hit:\033[0m Przywrócono stan slotu K/V z RAM")
    }

    # Generacja
    else if (match(raw, /n_gen =\s+([0-9]+)/, a)) {
        n_gen = a[1]
        if (match(raw, /tg =\s+([0-9.]+)/, b)) tg = b[1]
        if (match(raw, /tg_3s =\s+([0-9.]+)/, c)) tg3s = c[1]
    }

    # Timery
    else if (match(raw, /prompt eval time =\s+([0-9.]+ ms)/, a)) prompt_eval = a[1]
    else if (match(raw, /eval time =\s+([0-9.]+ ms)/, a)) eval_time = a[1]
    else if (match(raw, /total time =\s+([0-9.]+ ms)/, a)) total_time = a[1]

    # API (GIN)
    else if (match(raw, /\[GIN\].*\|\s*([0-9]{3})\s*\|\s*([^ ]+)\s*\|.*?((POST|GET|PUT|DELETE)\s+("[^"]+"))/, a)) {
        http_code = a[1]; http_time = a[2]; http_method = a[4]; http_path = a[5]
        push_event("\033[38;5;208mAPI Req:\033[0m " http_code " | " http_method " " http_path " w " http_time)
    }

    # --- MAIN RENDER LOOP (1 FPS) ---
    now = systime()
    if (now != last_render) {

        # Pobieranie asynchronicznego stanu API (Zabezpieczone pod błędy I/O)
        if ((getline api_line < (shm "/api.txt")) > 0) {
            split(api_line, ap, "|")
            api_model = ap[1]; api_size = ap[2]; api_vram = ap[3]; api_quant = ap[4]
        }
        close(shm "/api.txt")

        # Pobieranie asynchronicznego stanu ROCm
        if ((getline rocm_line < (shm "/rocm.txt")) > 0) {
            split(rocm_line, rp, "|")
            gpu_temp = rp[1]; gpu_load = rp[2]; gpu_vram = rp[3]
        }
        close(shm "/rocm.txt")

        # Kalkulacja Uptime
        up_sec = now - uptime_start
        uptime = sprintf("%02d:%02d:%02d", int(up_sec/3600), int((up_sec%3600)/60), up_sec%60)

        # Skok do początku i nadpisywanie
        printf "\033[H\033[0J"

        printf "\033[1;38;5;196m%s\033[0m \033[38;5;240m|\033[0m \033[1;37mUPTIME:\033[0m %s\n", "=== [ GENESIS DAEMON CONTROL ] ===", uptime
        printf "\033[38;5;238m%s\033[0m\n", "=================================================================================="

        # Blok 1: Hardware i API (Rzeczywisty Stan)
        printf "\033[1;38;5;39m[!] SYSTEM & HARDWARE (ROCm)\033[0m\n"
        printf "  \033[38;5;245mActive Model: \033[1;37m%-30s\033[0m \033[38;5;245mQuant: \033[38;5;114m%s\033[0m\n", api_model, api_quant
        printf "  \033[38;5;245mMemory Map:   \033[38;5;245mDisk:\033[1;37m %-6.2f GB\033[0m \033[38;5;240m|\033[0m \033[38;5;245mVRAM Locked:\033[1;33m %-6.2f GB\033[0m\n", api_size, api_vram

        # Kolorowanie obciążenia GPU
        c_gpu = "\033[1;32m"; if (gpu_load > 60) c_gpu = "\033[1;33m"; if (gpu_load > 90) c_gpu = "\033[1;31m"
        c_tmp = "\033[1;32m"; if (gpu_temp > 75) c_tmp = "\033[1;33m"; if (gpu_temp > 90) c_tmp = "\033[1;31m"
        printf "  \033[38;5;245mGPU Status:   \033[38;5;245mUse: %s%-3s%%\033[0m \033[38;5;240m|\033[0m \033[38;5;245mTemp: %s%-3s°C\033[0m \033[38;5;240m|\033[0m \033[38;5;245mVRAM Total Use:\033[1;35m %s%%\033[0m\n", c_gpu, gpu_load, c_tmp, gpu_temp, gpu_vram

        # Blok 2: LLM Engine
        printf "\n\033[1;38;5;118m[>] INFERENCE ENGINE (llama.cpp)\033[0m\n"
        printf "  \033[38;5;245mSampler:      \033[38;5;245mTemp:\033[1;37m%-5s\033[0m \033[38;5;245mTop-K:\033[1;37m%-5s\033[0m \033[38;5;245mTop-P:\033[1;37m%-5s\033[0m \033[38;5;245mCtxSlot:\033[1;37m%-8s\033[0m\n", temp, top_k, top_p, ctx_slot
        printf "  \033[38;5;245mPrompt:       \033[1;33m%-6s\033[0m \033[38;5;240m->\033[0m \033[1;37m%-8s\033[0m \033[38;5;245mt/s\033[0m\n", prog, tps
        printf "  \033[38;5;245mGeneration:   \033[1;32m%-6s\033[0m \033[38;5;240m->\033[0m \033[1;37m%-8s\033[0m \033[38;5;245mt/s (3s avg: %s t/s)\033[0m\n", n_gen, tg, tg3s
        printf "  \033[38;5;245mCtx Size:     \033[1;35m%-10s\033[0m \033[38;5;240m|\033[0m \033[38;5;245mEval Timers: P=\033[1;37m%s\033[0m, E=\033[1;37m%s\033[0m\n", ctx_size, prompt_eval, eval_time

        # Blok 3: Event Log
        printf "\n\033[1;38;5;141m[@] LIVE EVENT LOG\033[0m\n"
        for(i=1; i<=5; i++) {
            if(event_buf[i] != "") printf "  %s\n", event_buf[i]
        }

        fflush()
        last_render = now
    }
}'
