package main

import (
	"bytes"
	"fmt"
	"image"
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
	img, _ := vips.NewImageFromFile(os.Args[1])
	if img != nil {
		img.Close()
	}

	for _, path := range os.Args[1:] {
		fmt.Printf("\n=== %s ===\n", path)
		benchFullPipeline(path)
	}
}

type benchRun struct {
	current  []time.Duration
	proposed []time.Duration
}

func benchFullPipeline(path string) {
	iters := 3
	var runs []benchRun

	for i := 0; i < iters+1; i++ {
		c, p := runBoth(path)
		if i == 0 {
			continue // warmup
		}
		runs = append(runs, benchRun{current: c, proposed: p})
	}

	fmt.Println("\n  CURRENT (export full-res PNG):")
	printSteps(runs, func(r benchRun) []time.Duration { return r.current }, []string{
		"vips.NewImageFromFile", "AutoRotate", "RemoveMetadata",
		"ExportPng (full)", "png.Decode (full)", "blurhash.Encode",
	})

	fmt.Println("\n  PROPOSED (resize to 64px → export tiny PNG):")
	printSteps(runs, func(r benchRun) []time.Duration { return r.proposed }, []string{
		"vips.NewImageFromFile", "AutoRotate", "RemoveMetadata",
		"Copy", "Resize(64px)", "ExportPng (tiny)", "png.Decode (tiny)", "blurhash.Encode",
	})
}

func printSteps(runs []benchRun, getSteps func(benchRun) []time.Duration, names []string) {
	fmt.Printf("  %-28s %10s %10s %10s %10s\n", "stage", "min", "avg", "max", "iter1")
	fmt.Printf("  %-28s %10s %10s %10s %10s\n", "-----", "---", "---", "---", "-----")
	var totalMin time.Duration
	for idx, name := range names {
		var vals []time.Duration
		for _, r := range runs {
			steps := getSteps(r)
			if idx < len(steps) {
				vals = append(vals, steps[idx])
			}
		}
		if len(vals) == 0 {
			continue
		}
		minV, avgV, maxV := statsDurations(vals)
		fmt.Printf("  %-28s %10v %10v %10v %10v\n", name, round(minV), round(avgV), round(maxV), round(vals[0]))
		totalMin += minV
	}
	fmt.Printf("  %-28s %10v\n", "TOTAL", round(totalMin))
}

func runBoth(path string) (current, proposed []time.Duration) {
	current = runCurrent(path)
	proposed = runProposed(path)
	return
}

func runCurrent(path string) []time.Duration {
	var steps []time.Duration
	t := func() func() {
		start := time.Now()
		return func() { steps = append(steps, time.Since(start)) }
	}

	done := t()
	img, _ := vips.NewImageFromFile(path)
	done()
	if img == nil {
		return steps
	}
	defer img.Close()

	done = t()
	img.AutoRotate()
	done()

	done = t()
	img.RemoveMetadata()
	done()

	done = t()
	buf, _, _ := img.ExportPng(vips.NewPngExportParams())
	done()

	done = t()
	decoded, _ := png.Decode(bytes.NewReader(buf))
	done()

	done = t()
	if decoded != nil {
		blurhash.Encode(4, 3, decoded)
	}
	done()

	return steps
}

func runProposed(path string) []time.Duration {
	var steps []time.Duration
	t := func() func() {
		start := time.Now()
		return func() { steps = append(steps, time.Since(start)) }
	}

	done := t()
	img, _ := vips.NewImageFromFile(path)
	done()
	if img == nil {
		return steps
	}
	defer img.Close()

	done = t()
	img.AutoRotate()
	done()

	done = t()
	img.RemoveMetadata()
	done()

	done = t()
	thumb, _ := img.Copy()
	done()

	done = t()
	if thumb != nil && thumb.Width() > 64 {
		thumb.Resize(64.0/float64(thumb.Width()), vips.KernelLanczos3)
	}
	done()

	done = t()
	var buf []byte
	if thumb != nil {
		buf, _, _ = thumb.ExportPng(vips.NewPngExportParams())
	}
	done()

	done = t()
	var decoded image.Image
	if buf != nil {
		decoded, _ = png.Decode(bytes.NewReader(buf))
	}
	done()

	done = t()
	if decoded != nil {
		blurhash.Encode(4, 3, decoded)
	}
	done()

	if thumb != nil {
		thumb.Close()
	}
	return steps
}

func statsDurations(vals []time.Duration) (min, avg, max time.Duration) {
	min = vals[0]
	var sum time.Duration
	for _, d := range vals {
		sum += d
		if d < min {
			min = d
		}
		if d > max {
			max = d
		}
	}
	avg = sum / time.Duration(len(vals))
	return
}

func round(d time.Duration) time.Duration {
	if d < time.Microsecond {
		return d
	}
	if d < time.Millisecond {
		return d.Round(time.Microsecond)
	}
	return d.Round(time.Millisecond)
}
