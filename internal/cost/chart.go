package cost

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/eitanpo/agentry/internal/spend"
)

// The pictures --chart draws in place of the rows. None is the default: a table
// is what a roll-up is asked for, and a chart answers a narrower question about
// shape that only the time axes can be asked.
const (
	ChartNone     = "none"
	ChartLine     = "line"
	ChartCalendar = "calendar"
)

// Charts is every --chart value, in the order help and completion offer them.
var Charts = []string{ChartNone, ChartLine, ChartCalendar}

// chartHeight is how many text rows the line plot occupies. Five rows of braille
// give twenty vertical positions, which separates a day at the median from one
// at twice it — the distinction the plot exists to make.
const chartHeight = 5

// brailleDots maps a row and column inside one braille cell to the bit that
// lights that dot. A cell is two dots wide and four tall, which is what buys the
// plot four times the horizontal resolution of block characters.
var brailleDots = [4][2]rune{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// brailleBase is the first code point of the braille block; adding a dot mask to
// it gives the character with those dots raised.
const brailleBase = 0x2800

// axisLabelW is the width of the value labels down the left of the line plot.
const axisLabelW = 9

// renderChart draws the buckets as the named picture, or returns false where
// there is nothing a picture would say. The caller then prints the table, so a
// chart that cannot be drawn is never a silently empty screen.
func renderChart(kind string, bs []Bucket, opts Options) (string, bool) {
	switch kind {
	case ChartLine:
		return lineChart(bs, opts)
	case ChartCalendar:
		return calendarChart(bs, opts)
	}
	return "", false
}

// lineChart plots each bucket's dollars as a braille line, oldest at the left.
// Points are joined rather than left as dots: separate dots at this resolution
// read as scatter, and the question the plot answers is which way the spend is
// going.
func lineChart(bs []Bucket, opts Options) (string, bool) {
	if len(bs) < 2 {
		return "", false
	}
	vals := make([]float64, len(bs))
	var max float64
	for i, b := range bs {
		vals[i] = b.CostUSD
		if b.CostUSD > max {
			max = b.CostUSD
		}
	}
	if max <= 0 {
		return "", false
	}
	cells := chartWidth(opts.Width)
	if len(vals) > cells {
		vals = downsample(vals, cells)
	}

	// Two dot columns per cell, four dot rows per text row.
	dotW, dotH := len(vals)*2, chartHeight*4
	grid := make([][]bool, dotH)
	for i := range grid {
		grid[i] = make([]bool, dotW)
	}
	yOf := func(v float64) int {
		y := dotH - 1 - int(v/max*float64(dotH-1))
		if y < 0 {
			y = 0
		}
		return y
	}
	prevX, prevY := -1, 0
	for i, v := range vals {
		x, y := i*2, yOf(v)
		if prevX >= 0 {
			for sx := prevX; sx <= x; sx++ {
				t := float64(sx-prevX) / float64(x-prevX)
				grid[prevY+int(float64(y-prevY)*t)][sx] = true
			}
		}
		grid[y][x] = true
		prevX, prevY = x, y
	}

	line := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "31", Dark: "80"})
	axis := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	var out strings.Builder
	for r := 0; r < chartHeight; r++ {
		var row strings.Builder
		for c := 0; c < dotW; c += 2 {
			var mask rune
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					if c+dx < dotW && grid[r*4+dy][c+dx] {
						mask |= brailleDots[dy][dx]
					}
				}
			}
			row.WriteRune(brailleBase + mask)
		}
		label := spend.USD(max * float64(chartHeight-r) / float64(chartHeight))
		out.WriteString(axis.Render(padLeft(label, axisLabelW)+" │") + line.Render(row.String()) + "\n")
	}
	out.WriteString(axis.Render(fmt.Sprintf("%s %s → %s",
		strings.Repeat(" ", axisLabelW), bs[0].Key, bs[len(bs)-1].Key)) + "\n")
	return out.String(), true
}

// chartWidth is how many cells a picture may use, leaving the left labels their
// column. The same default as the table's: a width nobody reported is taken to
// be a hundred columns.
func chartWidth(width int) int {
	if width <= 0 {
		width = 100
	}
	w := width - axisLabelW - 2
	if w < 10 {
		w = 10
	}
	return w
}

// downsample averages the series into the cells available. Taking every nth
// point instead would drop the days the plot exists to show — a spike lands
// between samples as readily as on one.
func downsample(vals []float64, cells int) []float64 {
	out := make([]float64, cells)
	for i := range out {
		lo, hi := i*len(vals)/cells, (i+1)*len(vals)/cells
		if hi <= lo {
			hi = lo + 1
		}
		var sum float64
		for _, v := range vals[lo:hi] {
			sum += v
		}
		out[i] = sum / float64(hi-lo)
	}
	return out
}

// calendarShades run from the lightest spending day to the heaviest. A day
// inside the span that spent nothing is a dot, so an idle day and a day outside
// the window never read alike.
var calendarShades = []rune{'░', '▒', '▓', '█'}

const calendarIdle = '·'

// calendarChart lays the days out as a week per column and a weekday per row,
// each cell shaded by which quarter of the spending days it falls in. It shows
// the shape of a month, which a table of thirty rows holds but does not show.
func calendarChart(bs []Bucket, opts Options) (string, bool) {
	days := map[string]float64{}
	var dates []time.Time
	for _, b := range bs {
		t, err := time.ParseInLocation("2006-01-02", b.Key, time.Local)
		if err != nil {
			continue // an unplaceable day has no square on a calendar
		}
		days[b.Key] = b.CostUSD
		dates = append(dates, t)
	}
	if len(dates) < 2 {
		return "", false
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	first, last := dates[0], dates[len(dates)-1]
	// Start on the Monday of the first week so every row is one weekday.
	first = first.AddDate(0, 0, -((int(first.Weekday()) + 6) % 7))

	cuts := quartiles(days)
	shadeOf := func(v float64) rune {
		if v <= 0 {
			return calendarIdle
		}
		for i, c := range cuts {
			if v <= c {
				return calendarShades[i]
			}
		}
		return calendarShades[len(calendarShades)-1]
	}

	var mondays []time.Time
	for d := first; !d.After(last); d = d.AddDate(0, 0, 7) {
		mondays = append(mondays, d)
	}
	note := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	heat := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "94", Dark: "180"})

	var out strings.Builder
	var months strings.Builder
	prev := ""
	for _, m := range mondays {
		if name := m.Format("Jan"); name != prev {
			months.WriteString(name[:1])
			prev = name
		} else {
			months.WriteString(" ")
		}
	}
	out.WriteString(note.Render("      "+months.String()) + "\n")
	for i, name := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		var row strings.Builder
		for _, m := range mondays {
			d := m.AddDate(0, 0, i)
			if d.After(last) || d.Before(dates[0]) {
				row.WriteRune(' ')
				continue
			}
			row.WriteRune(shadeOf(days[d.Format("2006-01-02")]))
		}
		out.WriteString(note.Render("  "+name+" ") + heat.Render(row.String()) + "\n")
	}
	out.WriteString(note.Render(fmt.Sprintf("      %c none  %s  low to high, quarters of a spending day",
		calendarIdle, string(calendarShades))) + "\n")
	return out.String(), true
}

// quartiles are the three cuts that split the spending days into four groups of
// equal count. Equal counts rather than equal dollars: a month with one very
// large day would otherwise shade every other day alike.
func quartiles(days map[string]float64) []float64 {
	var vals []float64
	for _, v := range days {
		if v > 0 {
			vals = append(vals, v)
		}
	}
	sort.Float64s(vals)
	if len(vals) == 0 {
		return []float64{0, 0, 0}
	}
	at := func(f float64) float64 {
		i := int(f * float64(len(vals)))
		if i >= len(vals) {
			i = len(vals) - 1
		}
		return vals[i]
	}
	return []float64{at(0.25), at(0.5), at(0.75)}
}
