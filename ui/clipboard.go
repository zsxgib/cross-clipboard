package ui

import (
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/ntsd/cross-clipboard/pkg/crossclipboard"
	"github.com/ntsd/go-utils/pkg/stringutil"
	"github.com/rivo/tview"
)

func (v *View) newClipboardBox() tview.Primitive {
	table := tview.NewTable().
		SetFixed(1, 1)

	cc := v.CrossClipboard

	// active + completed file transfers keyed by filename+direction
	fileTransfers := make(map[string]crossclipboard.FileProgress)

	rebuild := func() {
		hiddenText := cc.Config.HiddenText

		table.Clear()

		// header
		table.SetCell(0, 0, tview.NewTableCell("time").SetTextColor(tcell.ColorYellow))
		table.SetCell(0, 1, tview.NewTableCell("size").SetTextColor(tcell.ColorYellow))
		table.SetCell(0, 2, tview.NewTableCell("type").SetTextColor(tcell.ColorYellow))
		if !hiddenText {
			table.SetCell(0, 3, tview.NewTableCell("text").SetTextColor(tcell.ColorYellow))
		}
		table.SetCell(0, 4, tview.NewTableCell("progress").SetTextColor(tcell.ColorYellow))
		table.SetCell(0, 5, tview.NewTableCell("speed").SetTextColor(tcell.ColorYellow))

		row := 1

		// file transfers (both active and completed)
		for _, fp := range fileTransfers {
			t := fp.Time
			if t.IsZero() {
				t = time.Now()
			}
			table.SetCell(row, 0, tview.NewTableCell(t.Format("15:04:05")))
			table.SetCell(row, 1, tview.NewTableCell(humanReadableSize(fp.Total)))

			dirStr := "file->"
			if fp.Direction == "recv" {
				dirStr = "file<-"
			}
			table.SetCell(row, 2, tview.NewTableCell(dirStr))

			if !hiddenText {
				table.SetCell(row, 3, tview.NewTableCell(stringutil.LimitStringLen(fp.FileName, 50)))
			}

			// progress column
			progText := progressBar(fp.Sent, fp.Total)
			if fp.Done {
				if fp.Err != "" {
					progText = "error"
				} else {
					progText = "done"
				}
			}
			table.SetCell(row, 4, tview.NewTableCell(progText))

			// speed column
			speedText := ""
			if !fp.Done && fp.Speed > 0 {
				speedText = humanReadableSpeed(fp.Speed)
			}
			table.SetCell(row, 5, tview.NewTableCell(speedText))

			row++
		}

		// clipboard history
		for _, clipboard := range cc.ClipboardManager.ClipboardsHistory {
			table.SetCell(row, 0, tview.NewTableCell(clipboard.Time.Format("15:04:05")))
			table.SetCell(row, 1, tview.NewTableCell(humanReadableSize(int64(clipboard.Size))))
			if clipboard.IsImage {
				table.SetCell(row, 2, tview.NewTableCell("image"))
			} else {
				table.SetCell(row, 2, tview.NewTableCell("text"))
			}
			if !hiddenText {
				if clipboard.IsImage {
					table.SetCell(row, 3, tview.NewTableCell(""))
				} else {
					text := stringutil.LimitStringLen(string(clipboard.Data), 100)
					table.SetCell(row, 3, tview.NewTableCell(text))
				}
			}
			table.SetCell(row, 4, tview.NewTableCell(""))
			table.SetCell(row, 5, tview.NewTableCell(""))
			row++
		}
	}

	go func() {
		for {
			select {
			case <-cc.ClipboardManager.ClipboardsHistoryUpdated:
				v.app.QueueUpdateDraw(rebuild)
			case fp := <-cc.FileProgressChan:
				v.app.QueueUpdateDraw(func() {
					key := fp.FileName + fp.Direction
					// keep completed entries (don't delete) so they show as "done"
					fileTransfers[key] = fp
					rebuild()
				})
			}
		}
	}()

	table.SetBorder(true).SetTitle("clipboards")

	return table
}
