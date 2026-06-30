package main

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"time"

	blurhash "github.com/bbrks/go-blurhash"
	vips "github.com/davidbyttow/govips/v2/vips"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: exif-bench <image.jpg> [image2.jpg]\n")
		os.Exit(1)
	}

	vips.Startup(nil)
	defer vips.Shutdown()
	// warmup
	ref, _ := vips.NewImageFromFile(os.Args[1])
	if ref != nil {
		ref.Close()
	}

	for _, path := range os.Args[1:] {
		benchSideBySide(path)
	}
}

func fmtDur(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%.0fµs", float64(d.Microseconds()))
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func benchSideBySide(path string) {
	img, _ := vips.NewImageFromFile(path)
	w, h := 0, 0
	if img != nil {
		w, h = img.Width(), img.Height()
		img.Close()
	}
	fi, _ := os.Stat(path)
	sizeMB := 0.0
	if fi != nil {
		sizeMB = float64(fi.Size()) / (1024 * 1024)
	}

	cu := runCurrent(path)
	fx := runFixed(path)
	if cu == nil || fx == nil {
		return
	}

	type stage struct {
		name string
		cur  time.Duration
		fix  time.Duration
	}
	stages := []stage{
		{"NewImageFromFile", cu[1], fx[1]},
		{"AutoRotate", cu[2] - cu[1], fx[2] - fx[1]},
		{"RemoveMetadata", cu[3] - cu[2], fx[3] - fx[2]},
		{"Copy + Resize(64px)", 0, fx[5] - fx[3]},
		{"ExportPng", cu[5] - cu[4], fx[6] - fx[5]},
		{"png.Decode", cu[6] - cu[5], fx[7] - fx[6]},
		{"blurhash.Encode(4,3)", cu[7] - cu[6], fx[8] - fx[7]},
	}

	fmt.Printf("\n┌──────────────────────────────────────────────────────────────────────────────┐\n")
	fmt.Printf("│  Image: %dx%d  ·  %.1f MB                                                  │\n", w, h, sizeMB)
	fmt.Printf("├──────────────────────────────────────────────────────────────────────────────┤\n")
	fmt.Printf("│  %-34s │ %10s │ %10s │ %8s │\n", "Stage", "Current", "Fixed", "Speedup")
	fmt.Printf("├──────────────────────────────────────────────────────────────────────────────┤\n")

	var totalCur, totalFix time.Duration
	for _, s := range stages {
		totalCur += s.cur
		totalFix += s.fix
		sp := "-"
		if s.cur > 0 && s.fix > 0 {
			sp = fmt.Sprintf("%.0fx", float64(s.cur)/float64(s.fix))
		}
		fmt.Printf("│  %-34s │ %10s │ %10s │ %8s │\n", s.name, fmtDur(s.cur), fmtDur(s.fix), sp)
	}

	spTotal := "-"
	if totalCur > 0 && totalFix > 0 {
		spTotal = fmt.Sprintf("%.0fx", float64(totalCur)/float64(totalFix))
	}
	fmt.Printf("├──────────────────────────────────────────────────────────────────────────────┤\n")
	fmt.Printf("│  %-34s │ %10s │ %10s │ %8s │\n", "TOTAL", fmtDur(totalCur), fmtDur(totalFix), spTotal)
	fmt.Printf("└──────────────────────────────────────────────────────────────────────────────┘\n")
}

// runCurrent: cumulative deltas [load, rotate, strip, pre-export, post-export, post-decode, post-blurhash]
func runCurrent(path string) []time.Duration {
	var marks []time.Time
	marks = append(marks, time.Now()) // baseline
	tick := func() { marks = append(marks, time.Now()) }

	ref, err := vips.NewImageFromFile(path)
	tick()
	if err != nil || ref == nil {
		return nil
	}

	_ = ref.AutoRotate()
	tick()
	_ = ref.RemoveMetadata()
	tick()
	tick() // pre-export = same as post-strip
	buf, _, _ := ref.ExportPng(vips.NewPngExportParams())
	tick()
	dec, _ := png.Decode(bytes.NewReader(buf))
	tick()
	if dec != nil {
		blurhash.Encode(4, 3, dec)
	}
	tick()
	ref.Close()

	t0 := marks[0]
	out := make([]time.Duration, len(marks))
	for i, m := range marks {
		out[i] = m.Sub(t0)
	}
	return out
}

// runFixed: cumulative deltas [load, rotate, strip, copy, post-resize, post-export, post-decode, post-blurhash]
func runFixed(path string) []time.Duration {
	var marks []time.Time
	marks = append(marks, time.Now()) // baseline
	tick := func() { marks = append(marks, time.Now()) }

	ref, _ := vips.NewImageFromFile(path)
	tick()
	if ref == nil {
		return nil
	}

	_ = ref.AutoRotate()
	tick()
	_ = ref.RemoveMetadata()
	tick()

	thumb, _ := ref.Copy()
	tick()
	if thumb.Width() > 64 {
		_ = thumb.Resize(64.0/float64(thumb.Width()), vips.KernelLanczos3)
	}
	tick()

	buf, _, _ := thumb.ExportPng(vips.NewPngExportParams())
	tick()
	dec, _ := png.Decode(bytes.NewReader(buf))
	tick()
	if dec != nil {
		blurhash.Encode(4, 3, dec)
	}
	tick()
	thumb.Close()
	ref.Close()

	t0 := marks[0]
	out := make([]time.Duration, len(marks))
	for i, m := range marks {
		out[i] = m.Sub(t0)
	}
	return out
}
