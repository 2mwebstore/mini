package schedule

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/attendance"

	"github.com/xuri/excelize/v2"
)

// Export renders the roster as xlsx (colour-coded matrix + shift list) or csv.
func Export(r *Roster, format string) (data []byte, filename, contentType string, err error) {
	base := fmt.Sprintf("roster_%s_%s", r.From.Format("20060102"), r.To.Format("20060102"))
	switch strings.ToLower(format) {
	case "csv":
		data, err = exportCSV(r)
		return data, base + ".csv", "text/csv; charset=utf-8", err
	case "xlsx", "excel", "":
		data, err = exportXLSX(r)
		return data, base + ".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", err
	}
	return nil, "", "", apperr.BadRequest("INVALID_FORMAT", "Format must be xlsx or csv.")
}

func header(r *Roster) []string {
	h := []string{"Employee ID", "Employee Name", "Branch", "Department", "Usual Shift"}
	for _, d := range r.Days {
		h = append(h, dayLabel(d))
	}
	return append(h, "Working Days")
}

func rowValues(row RosterRow) []string {
	v := []string{row.EmployeeCode, row.FullName, row.Branch, row.Department, row.UsualShift}
	work := 0
	for _, c := range row.Cells {
		v = append(v, cellText(c))
		if c.Work && c.Holiday == "" && c.Leave == "" {
			work++
		}
	}
	return append(v, strconv.Itoa(work))
}

// safe neutralises spreadsheet formula injection from user-controlled text.
func safe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func exportCSV(r *Roster) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // BOM so Excel opens UTF-8 (Khmer names) correctly
	w := csv.NewWriter(&buf)
	_ = w.Write(header(r))
	for _, row := range r.Rows {
		v := rowValues(row)
		for i := range v {
			v[i] = safe(v[i])
		}
		_ = w.Write(v)
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

func exportXLSX(r *Roster) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Roster"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	title := fmt.Sprintf("Work schedule %s – %s", r.From.Format("02 Jan 2006"), r.To.Format("02 Jan 2006"))
	_ = f.SetCellValue(sheet, "A1", title)
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 13}})
	_ = f.SetCellStyle(sheet, "A1", "A1", bold)
	_ = f.SetCellValue(sheet, "A2", "* special schedule   (+1) ends the next day")

	h := header(r)
	head, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#E5E7EB"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true}, Border: borders()})
	for i, v := range h {
		cell, _ := excelize.CoordinatesToCellName(i+1, 4)
		_ = f.SetCellValue(sheet, cell, v)
		_ = f.SetCellStyle(sheet, cell, cell, head)
	}
	styles := map[string]int{}
	style := func(fill string) int {
		if id, ok := styles[fill]; ok {
			return id
		}
		st := &excelize.Style{Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true}, Border: borders()}
		if fill != "" {
			st.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{fill}}
		}
		id, _ := f.NewStyle(st)
		styles[fill] = id
		return id
	}
	plain := style("")
	for ri, row := range r.Rows {
		y := ri + 5
		for ci, v := range rowValues(row) {
			cell, _ := excelize.CoordinatesToCellName(ci+1, y)
			if ci == len(h)-1 {
				n, _ := strconv.Atoi(v)
				_ = f.SetCellValue(sheet, cell, n)
			} else {
				_ = f.SetCellValue(sheet, cell, safe(v))
			}
			_ = f.SetCellStyle(sheet, cell, cell, plain)
		}
		for di, c := range row.Cells {
			cell, _ := excelize.CoordinatesToCellName(6+di, y)
			_ = f.SetCellStyle(sheet, cell, cell, style(cellFill(c)))
		}
	}
	_ = f.SetColWidth(sheet, "A", "A", 12)
	_ = f.SetColWidth(sheet, "B", "B", 24)
	_ = f.SetColWidth(sheet, "C", "E", 16)
	last, _ := excelize.ColumnNumberToName(5 + len(r.Days))
	_ = f.SetColWidth(sheet, "F", last, 20)
	_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, XSplit: 2, YSplit: 4, TopLeftCell: "C5", ActivePane: "bottomRight"})

	// Second sheet: what each shift means.
	const legend = "Shifts"
	if _, err := f.NewSheet(legend); err != nil {
		return nil, err
	}
	for i, v := range []string{"Shift", "Start", "End", "Work type", "Grace (min)", "Usual work days"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(legend, cell, v)
		_ = f.SetCellStyle(legend, cell, cell, head)
	}
	for i, s := range r.Shifts {
		y := i + 2
		wt := "Normal (same day)"
		if s.WorkType == "CROSS_DAY" {
			wt = "Cross-day (ends next day)"
		}
		vals := []any{safe(s.Name), s.StartTime, s.EndTime, wt, s.GraceMinutes, weekdays(s.WorkDays)}
		for ci, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(ci+1, y)
			_ = f.SetCellValue(legend, cell, v)
		}
		cell, _ := excelize.CoordinatesToCellName(1, y)
		_ = f.SetCellStyle(legend, cell, cell, style(tint(s.Color)))
	}
	_ = f.SetColWidth(legend, "A", "A", 24)
	_ = f.SetColWidth(legend, "D", "D", 26)
	_ = f.SetColWidth(legend, "F", "F", 30)

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func borders() []excelize.Border {
	return []excelize.Border{{Type: "left", Color: "D1D5DB", Style: 1}, {Type: "right", Color: "D1D5DB", Style: 1},
		{Type: "top", Color: "D1D5DB", Style: 1}, {Type: "bottom", Color: "D1D5DB", Style: 1}}
}

func cellFill(c RosterCell) string {
	switch {
	case c.Leave != "":
		return "#DBEAFE"
	case c.Holiday != "" && c.Work:
		return "#FEF3C7"
	case !c.Work:
		return "#F3F4F6"
	}
	return tint(c.Color)
}

// tint lightens a #rrggbb colour (75% towards white) for a readable cell fill.
func tint(hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return ""
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return ""
	}
	mix := func(c uint64) uint64 { return c + (255-c)*3/4 }
	return fmt.Sprintf("#%02X%02X%02X", mix(v>>16&0xff), mix(v>>8&0xff), mix(v&0xff))
}

func weekdays(csv string) string {
	set := attendance.ParseWorkDays(csv)
	var names []string
	for d := 1; d <= 7; d++ {
		if set[d] {
			names = append(names, weekdayNames[d-1])
		}
	}
	return strings.Join(names, ", ")
}
