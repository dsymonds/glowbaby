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

func plot(ctx context.Context, db *sql.DB, typ string) ([]byte, error) {
	switch typ {
	default:
		// Shouldn't happen; main.go should filter things out.
		return nil, fmt.Errorf("unknown plot type %q", typ)
	case "sleep":
		return plotSleep(ctx, db)
	case "feed":
		return plotFeed(ctx, db)
	}
}

type babyInfo struct {
	babyID              int64
	firstName, lastName string
	birthday            time.Time
}

func loadOneBaby(ctx context.Context, db *sql.DB) (babyInfo, error) {
	// TODO: record baby timezone from Glow and use that instead of time.Local below.
	row := db.QueryRowContext(ctx, `SELECT BabyID, FirstName, LastName, Birthday FROM Babies LIMIT 1`)
	var info babyInfo
	var bday string
	err := row.Scan(&info.babyID, &info.firstName, &info.lastName, &bday)
	if err != nil {
		return babyInfo{}, fmt.Errorf("loading baby info: %w", err)
	}
	info.birthday, err = time.ParseInLocation("2006-01-02", bday, time.Local)
	if err != nil {
		return babyInfo{}, fmt.Errorf("parsing baby birthday %q: %w", bday, err)
	}
	return info, nil
}

type polarPlot struct {
	segments  [][2]int64 // start, end unix epoch
	title     string
	zero      time.Time // Centre of the circle (e.g. birthday).
	colSelect func(startD, endD int, startFrac, endFrac float64) color.NRGBA
}

func (pp *polarPlot) AddSegment(start, end int64) {
	pp.segments = append(pp.segments, [2]int64{start, end})
}

func plotSleep(ctx context.Context, db *sql.DB) ([]byte, error) {
	// Load baby info.
	// TODO: Handle multiple babies.
	info, err := loadOneBaby(ctx, db)
	if err != nil {
		return nil, err
	}
	log.Printf("Selected %s %s (born %s) for sleep plotting", info.firstName, info.lastName, info.birthday.Format("2006-01-02"))

	// Load sleep data.
	var pp polarPlot
	rows, err := db.QueryContext(ctx, `
		SELECT StartTimestamp, EndTimestamp FROM BabyData
		WHERE BabyID = ? AND Key = "sleep" ORDER BY StartTimestamp`, info.babyID)
	if err != nil {
		return nil, fmt.Errorf("loading sleep ranges: %w", err)
	}
	for rows.Next() {
		var start, end int64
		if err := rows.Scan(&start, &end); err != nil {
			return nil, fmt.Errorf("scanning sleep ranges from DB: %w", err)
		}
		pp.AddSegment(start, end)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading sleep ranges from DB: %w", err)
	}
	log.Printf("Loaded %d sleep ranges", len(pp.segments))

	if len(pp.segments) == 0 {
		log.Fatalf("Sorry, can't plot without any sleep recorded!")
	}

	pp.title = fmt.Sprintf("Sleep segments for %s %s (born %s)", info.firstName, info.lastName, info.birthday.Format("2006-01-02"))
	pp.zero = info.birthday
	pp.colSelect = func(startD, endD int, startFrac, endFrac float64) color.NRGBA {
		hours := (endFrac-startFrac)*24 + float64(endD-startD)*24
		switch {
		case hours >= 5:
			return color.NRGBA{0, 0, 255, 255} // blue
		case hours >= 1.5:
			return color.NRGBA{0, 255, 0, 255} // green
		default:
			return color.NRGBA{255, 0, 0, 255} // red
		}
	}

	return pp.Render()
}

func plotFeed(ctx context.Context, db *sql.DB) ([]byte, error) {
	// Load baby info.
	// TODO: Handle multiple babies.
	info, err := loadOneBaby(ctx, db)
	if err != nil {
		return nil, err
	}
	log.Printf("Selected %s %s (born %s) for feed plotting", info.firstName, info.lastName, info.birthday.Format("2006-01-02"))

	// Load feed data.
	// Only start timestamp and per-breast times are available.
	// TODO: Include bottle feeding too somehow. Maybe that has end timestamps?
	var pp polarPlot
	rows, err := db.QueryContext(ctx, `
		SELECT StartTimestamp, BreastLeft, BreastRight FROM BabyFeedData
		WHERE BabyID = ? ORDER BY StartTimestamp`, info.babyID)
	if err != nil {
		return nil, fmt.Errorf("loading feeds: %w", err)
	}
	for rows.Next() {
		var start, left, right int64
		if err := rows.Scan(&start, &left, &right); err != nil {
			return nil, fmt.Errorf("scanning feeds from DB: %w", err)
		}
		pp.AddSegment(start, start+left+right)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading feeds from DB: %w", err)
	}
	log.Printf("Loaded %d feeds", len(pp.segments))

	if len(pp.segments) == 0 {
		log.Fatalf("Sorry, can't plot without any feeds recorded!")
	}

	pp.title = fmt.Sprintf("Feeds for %s %s (born %s)", info.firstName, info.lastName, info.birthday.Format("2006-01-02"))
	pp.zero = info.birthday
	pp.colSelect = func(startD, endD int, startFrac, endFrac float64) color.NRGBA {
		// All blue, except for midnight-spanning feeds.
		if startD == endD {
			return color.NRGBA{0, 0, 255, 255} // blue
		}
		return color.NRGBA{255, 0, 0, 255} // red
	}

	return pp.Render()
}

func (pp *polarPlot) Render() ([]byte, error) {
	// Initialise an all-white image.
	img := image.NewNRGBA(image.Rect(0, 0, plotImageWidth, plotImageHeight))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.ZP, draw.Src)

	// Add a title.
	err := writeText(img, 5, 5+plotTextSize, pp.title)
	if err != nil {
		log.Printf("Writing text: %v", err)
		// Continue anyway. This was likely a font-loading issue.
	}

	// Plot data.
	// Each segment is drawn as an arc, where midnight is at the top,
	// and days extend from the circle centre outwards.
	// Segments spanning midnight will
	splitEpoch := func(x int64) (day int, frac float64) {
		t := time.Unix(x, 0).In(time.Local)
		day = dayDiff(pp.zero, t)
		h, m, s := t.Clock()
		frac = float64(h)/24 + float64(m)/(24*60) + float64(s)/(24*60*60)
		return
	}
	maxDay, _ := splitEpoch(pp.segments[len(pp.segments)-1][1])
	dayScale := float64(plotImageHeight) / 2 * 0.9 / float64(maxDay)
	for _, seg := range pp.segments {
		startD, startFrac := splitEpoch(seg[0])
		endD, endFrac := splitEpoch(seg[1])

		col := pp.colSelect(startD, endD, startFrac, endFrac)

		if endFrac < startFrac {
			// This crosses a midnight.
			endFrac += float64(endD - startD)
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

// dayDiff reports the number of calendar days between the given times.
// Zero means start and end are on the same day.
func dayDiff(start, end time.Time) (days int) {
	if start.After(end) {
		panic("start after end")
	}

	// Extract the calendar dates in the correct time zone, then do the computation in UTC,
	// which is simply dividing the unix epoch difference by 86400.
	sY, sM, sD := start.Date()
	eY, eM, eD := end.Date()
	s0 := time.Date(sY, sM, sD, 0, 0, 0, 0, time.UTC)
	e0 := time.Date(eY, eM, eD, 0, 0, 0, 0, time.UTC)

	return int(e0.Unix()-s0.Unix()) / 86400
}
