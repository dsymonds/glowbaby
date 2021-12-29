package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/ioutil"
	"log"
	"math"
	"time"

	"github.com/golang/freetype"
)

const (
	// TODO: flags for these?
	plotImageWidth  = 1024 // pixels
	plotImageHeight = 768  // pixels
	plotTextSize    = 16   // points
)

func plotSleep(ctx context.Context, db *sql.DB) ([]byte, error) {
	// TODO: record baby timezone from Glow and use that instead of time.Local below.

	// Load baby info.
	// TODO: Handle multiple babies.
	row := db.QueryRowContext(ctx, `SELECT BabyID, FirstName, LastName, Birthday FROM Babies LIMIT 1`)
	var (
		babyID                    int64
		firstName, lastName, bday string
	)
	if err := row.Scan(&babyID, &firstName, &lastName, &bday); err != nil {
		return nil, fmt.Errorf("determining baby to plot: %w", err)
	}
	log.Printf("Selected %s %s (born %s) for sleep plotting", firstName, lastName, bday)
	birthday, err := time.ParseInLocation("2006-01-02", bday, time.Local)
	if err != nil {
		return nil, fmt.Errorf("parsing baby birthday %q: %w", bday, err)
	}

	// Load sleep data.
	var sleepRanges [][2]int64 // start, end unix epoch
	rows, err := db.QueryContext(ctx, `
		SELECT StartTimestamp, EndTimestamp FROM BabyData
		WHERE BabyID = ? AND Key = "sleep" ORDER BY StartTimestamp`, babyID)
	if err != nil {
		return nil, fmt.Errorf("loading sleep ranges: %w", err)
	}
	for rows.Next() {
		var start, end int64
		if err := rows.Scan(&start, &end); err != nil {
			return nil, fmt.Errorf("scanning sleep ranges from DB: %w", err)
		}
		sleepRanges = append(sleepRanges, [2]int64{start, end})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading sleep ranges from DB: %w", err)
	}
	log.Printf("Loaded %d sleep ranges", len(sleepRanges))

	if len(sleepRanges) == 0 {
		log.Fatalf("Sorry, can't plot without any sleep recorded!")
	}

	// Initialise an all-white image.
	var (
		white = color.NRGBA{255, 255, 255, 255}
		blue  = color.NRGBA{0, 0, 255, 255}
		green = color.NRGBA{0, 255, 0, 255}
		red   = color.NRGBA{255, 0, 0, 255}
	)
	img := image.NewNRGBA(image.Rect(0, 0, plotImageWidth, plotImageHeight))
	draw.Draw(img, img.Bounds(), &image.Uniform{white}, image.ZP, draw.Src)

	// Add a title.
	err = writeText(img, 5, 5+plotTextSize, fmt.Sprintf("Sleep segments for %s %s (born %s)", firstName, lastName, bday))
	if err != nil {
		log.Printf("Writing text: %v", err)
		// Continue anyway. This was likely a font-loading issue.
	}

	// Plot data.
	// Each sleep segment is drawn as an arc, where midnight is at the top,
	// and days extend from the circle centre outwards.
	// Segments spanning midnight will
	splitEpoch := func(x int64) (day int, frac float64) {
		t := time.Unix(x, 0).In(time.Local)
		day = int(t.Sub(birthday) / (24 * time.Hour))
		h, m, s := t.Clock()
		frac = float64(h)/24 + float64(m)/(24*60) + float64(s)/(24*60*60)
		return
	}
	maxDay, _ := splitEpoch(sleepRanges[len(sleepRanges)-1][1])
	dayScale := float64(plotImageHeight) / 2 * 0.9 / float64(maxDay)
	for _, sr := range sleepRanges {
		startD, startFrac := splitEpoch(sr[0])
		endD, endFrac := splitEpoch(sr[1])
		hours := float64(sr[1]-sr[0]) / 3600

		if endFrac < startFrac {
			// This crosses a midnight.
			endFrac += float64(endD - startD)
		}

		var col color.NRGBA
		switch {
		case hours >= 5:
			col = blue
		case hours >= 1.5:
			col = green
		default:
			col = red
		}

		for step := 0.0; step <= 1.0; step += 0.0001 { // TODO: adaptive
			d := dayScale * (float64(startD) + float64(endD-startD)*step)
			frac := startFrac + (endFrac-startFrac)*step
			theta := frac * 2 * math.Pi

			// Start at top, go clockwise.
			x := plotImageWidth/2 + d*math.Sin(theta)
			y := plotImageHeight/2 + d*-math.Cos(theta)
			img.SetNRGBA(int(x), int(y), col)
		}
	}

	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encoding PNG: %w", err)
	}
	return buf.Bytes(), nil
}

func writeText(img *image.NRGBA, x, y int, text string) error {
	// TODO: have a list of fonts to load.
	fdata, err := ioutil.ReadFile("/System/Library/Fonts/SFNS.ttf")
	if err != nil {
		return fmt.Errorf("loading font file: %w", err)
	}
	font, err := freetype.ParseFont(fdata)
	if err != nil {
		return fmt.Errorf("parsing font data: %w", err)
	}
	ctx := freetype.NewContext()
	ctx.SetDst(img)
	ctx.SetDPI(72)
	ctx.SetClip(img.Bounds())
	ctx.SetFont(font)
	ctx.SetFontSize(plotTextSize)
	ctx.SetSrc(&image.Uniform{color.Black})
	_, err = ctx.DrawString(text, freetype.Pt(x, y))
	return err
}
