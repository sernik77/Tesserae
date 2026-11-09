package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Dim is one rectangle coordinate. It is either an absolute cell count or a
// percentage of the screen with an optional cell offset ("50%", "50%+2").
//
// Positions (x, y): a negative absolute value counts from the right/bottom edge.
// Sizes (w, h): a zero or negative absolute value means "stretch to the edge
// minus N cells".
type Dim struct {
	Pct   float64
	Off   int
	IsPct bool
}

// Cells returns an absolute Dim.
func Cells(n int) Dim { return Dim{Off: n} }

// Percent returns a percentage Dim.
func Percent(p float64) Dim { return Dim{Pct: p, IsPct: true} }

// ParseDim parses "12", "-3", "50%", "33.3%-1".
func ParseDim(s string) (Dim, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	if s == "" {
		return Dim{}, fmt.Errorf("empty dimension")
	}
	i := strings.IndexByte(s, '%')
	if i < 0 {
		n, err := strconv.Atoi(s)
		if err != nil {
			return Dim{}, fmt.Errorf("invalid dimension %q (use 12, -3, 50%% or 50%%+2)", s)
		}
		return Dim{Off: n}, nil
	}
	p, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return Dim{}, fmt.Errorf("invalid percentage in %q", s)
	}
	d := Dim{Pct: p, IsPct: true}
	if rest := s[i+1:]; rest != "" {
		n, err := strconv.Atoi(rest)
		if err != nil || (rest[0] != '+' && rest[0] != '-') {
			return Dim{}, fmt.Errorf("invalid offset in %q", s)
		}
		d.Off = n
	}
	return d, nil
}

// String renders the Dim in the same syntax ParseDim accepts.
func (d Dim) String() string {
	if !d.IsPct {
		return strconv.Itoa(d.Off)
	}
	s := strconv.FormatFloat(d.Pct, 'f', -1, 64) + "%"
	if d.Off > 0 {
		s += "+" + strconv.Itoa(d.Off)
	} else if d.Off < 0 {
		s += strconv.Itoa(d.Off)
	}
	return s
}

// UnmarshalTOML accepts both integers and strings.
func (d *Dim) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case int64:
		*d = Dim{Off: int(t)}
	case float64:
		*d = Dim{Off: int(math.Round(t))}
	case string:
		p, err := ParseDim(t)
		if err != nil {
			return err
		}
		*d = p
	default:
		return fmt.Errorf("dimension must be an integer or a string, got %T", v)
	}
	return nil
}

// MarshalTOML writes absolute values as integers and percentages as strings.
func (d Dim) MarshalTOML() ([]byte, error) {
	if !d.IsPct {
		return []byte(strconv.Itoa(d.Off)), nil
	}
	return []byte(strconv.Quote(d.String())), nil
}

// raw returns the unrounded edge value against a screen extent.
func (d Dim) raw(extent int) float64 {
	if d.IsPct {
		return d.Pct*float64(extent)/100 + float64(d.Off)
	}
	return float64(d.Off)
}

// ResolveSpan resolves a position/size pair against a screen extent and
// returns the absolute start cell and size. Edges are rounded, not sizes, so
// that adjacent percentage elements always share an edge without gaps.
func ResolveSpan(pos, size Dim, extent int) (int, int) {
	start := pos.raw(extent)
	if !pos.IsPct && pos.Off < 0 {
		start = float64(extent + pos.Off)
	}
	var end float64
	switch {
	case size.IsPct:
		end = start + size.raw(extent)
	case size.Off <= 0:
		end = float64(extent + size.Off)
	default:
		end = start + float64(size.Off)
	}
	s := int(math.Round(start))
	e := int(math.Round(end))
	if e < s {
		e = s
	}
	return s, e - s
}

// Rebase returns a position Dim of the same kind as d that resolves to cell.
func (d Dim) Rebase(cell, extent int) Dim {
	switch {
	case d.IsPct:
		if extent <= 0 {
			return d
		}
		p := float64(cell-d.Off) * 100 / float64(extent)
		ok := func(r float64) bool { return int(math.Round(r*float64(extent)/100+float64(d.Off))) == cell }
		return Dim{Pct: roundPct(p, ok), Off: d.Off, IsPct: true}
	case d.Off < 0:
		if v := cell - extent; v < 0 {
			return Dim{Off: v}
		}
		return Dim{Off: cell}
	default:
		return Dim{Off: cell}
	}
}

// StartRaw returns the unrounded start edge of a position Dim.
func (d Dim) StartRaw(extent int) float64 {
	if !d.IsPct && d.Off < 0 {
		return float64(extent + d.Off)
	}
	return d.raw(extent)
}

// RebaseSize returns a size Dim of the same kind as d so that an element
// positioned by pos resolves to start and size cells.
func (d Dim) RebaseSize(pos Dim, start, size, extent int) Dim {
	switch {
	case d.IsPct:
		if extent <= 0 {
			return d
		}
		sraw := pos.StartRaw(extent)
		end := start + size
		p := (float64(end) - sraw - float64(d.Off)) * 100 / float64(extent)
		ok := func(r float64) bool {
			return int(math.Round(sraw+r*float64(extent)/100+float64(d.Off))) == end
		}
		return Dim{Pct: roundPct(p, ok), Off: d.Off, IsPct: true}
	case d.Off <= 0:
		if v := start + size - extent; v <= 0 {
			return Dim{Off: v}
		}
		return Dim{Off: size}
	default:
		return Dim{Off: size}
	}
}

// roundPct rounds a percentage to the fewest decimals that still satisfy ok.
func roundPct(p float64, ok func(float64) bool) float64 {
	for dec := 0; dec <= 4; dec++ {
		m := math.Pow(10, float64(dec))
		if r := math.Round(p*m) / m; ok(r) {
			return r
		}
	}
	return math.Round(p*10000) / 10000
}
