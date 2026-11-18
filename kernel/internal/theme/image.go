package theme

import (
	"image"
	_ "image/gif"  // logo formats
	_ "image/jpeg" // logo formats
	_ "image/png"  // logo formats
	"io"
	"math"
	"sort"
)

// Contract 0067 §6 POST /themes/from-image: the main colours of a logo, computed (not guessed by an LLM):
// opaque pixels on a grid of at most 64×64 samples, k-means (k ≤ 5) in CIE Lab with a deterministic start,
// near-white and near-black clusters dropped unless they are all there is, largest first.

type lab struct{ L, A, B float64 }

func lin(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func toLab(r, g, b float64) lab {
	r, g, b = lin(r), lin(g), lin(b)
	x := (0.4124*r + 0.3576*g + 0.1805*b) / 0.95047
	y := 0.2126*r + 0.7152*g + 0.0722*b
	z := (0.0193*r + 0.1192*g + 0.9505*b) / 1.08883
	f := func(t float64) float64 {
		if t > 0.008856 {
			return math.Cbrt(t)
		}
		return 7.787*t + 16.0/116.0
	}
	fx, fy, fz := f(x), f(y), f(z)
	return lab{116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)}
}

func dist(a, b lab) float64 {
	return (a.L-b.L)*(a.L-b.L) + (a.A-b.A)*(a.A-b.A) + (a.B-b.B)*(a.B-b.B)
}

type sample struct {
	lab lab
	rgb [3]float64
}

// Palette returns up to 5 colours of the image, largest share first, and the suggested accent: the most
// colourful of them (chroma ≥ 15), else the first.
func Palette(r io.Reader) (colours []string, suggested string, err error) {
	img, _, err := image.Decode(r)
	if err != nil {
		return nil, "", err
	}
	b := img.Bounds()
	step := int(math.Max(1, math.Ceil(float64(max(b.Dx(), b.Dy()))/64)))
	var px []sample
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			cr, cg, cb, ca := img.At(x, y).RGBA()
			if ca < 0x8000 {
				continue
			}
			// un-premultiply
			rf, gf, bf := float64(cr)/float64(ca), float64(cg)/float64(ca), float64(cb)/float64(ca)
			px = append(px, sample{lab: toLab(rf, gf, bf), rgb: [3]float64{rf, gf, bf}})
		}
	}
	if len(px) == 0 {
		return nil, "", nil
	}
	k := min(5, len(px))
	// deterministic start: the first pixel, then each next centre the pixel farthest from the chosen ones
	centres := []lab{px[0].lab}
	for len(centres) < k {
		best, bd := 0, -1.0
		for i, p := range px {
			d := math.Inf(1)
			for _, c := range centres {
				d = math.Min(d, dist(p.lab, c))
			}
			if d > bd {
				best, bd = i, d
			}
		}
		if bd <= 0 {
			break
		}
		centres = append(centres, px[best].lab)
	}
	k = len(centres)
	assign := make([]int, len(px))
	for iter := 0; iter < 12; iter++ {
		for i, p := range px {
			bi, bd := 0, math.Inf(1)
			for j, c := range centres {
				if d := dist(p.lab, c); d < bd {
					bi, bd = j, d
				}
			}
			assign[i] = bi
		}
		sum := make([]lab, k)
		n := make([]int, k)
		for i, p := range px {
			j := assign[i]
			sum[j].L += p.lab.L
			sum[j].A += p.lab.A
			sum[j].B += p.lab.B
			n[j]++
		}
		for j := range centres {
			if n[j] > 0 {
				centres[j] = lab{sum[j].L / float64(n[j]), sum[j].A / float64(n[j]), sum[j].B / float64(n[j])}
			}
		}
	}
	// each cluster's colour is the average of its pixels in sRGB (no Lab→sRGB round trip), with its share
	type cl struct {
		n       int
		rgb     [3]float64
		l, chro float64
	}
	cls := make([]cl, k)
	for i, p := range px {
		c := &cls[assign[i]]
		c.n++
		for t := 0; t < 3; t++ {
			c.rgb[t] += p.rgb[t]
		}
	}
	var kept, all []cl
	for j := range cls {
		c := cls[j]
		if c.n == 0 {
			continue
		}
		for t := 0; t < 3; t++ {
			c.rgb[t] /= float64(c.n)
		}
		c.l, c.chro = centres[j].L, math.Hypot(centres[j].A, centres[j].B)
		all = append(all, c)
		if c.l > 95 || c.l < 8 {
			continue
		}
		kept = append(kept, c)
	}
	if len(kept) == 0 {
		kept = all
	}
	sort.SliceStable(kept, func(a, b int) bool { return kept[a].n > kept[b].n })
	bestChroma := -1.0
	for _, c := range kept {
		h := toHex(c.rgb[0], c.rgb[1], c.rgb[2])
		colours = append(colours, h)
		if c.chro >= 15 && c.chro > bestChroma {
			suggested, bestChroma = h, c.chro
		}
	}
	if suggested == "" && len(colours) > 0 {
		suggested = colours[0]
	}
	return colours, suggested, nil
}
