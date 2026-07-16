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

	// active file transfers keyed by filename+direction
	fileTransfers := make(map[string]crossclipboard.FileProgress)

	rebuild := func() {
		hiddenText := cc.Config.HiddenText

		table.Clear()

		table.SetCell(0, 0, tview.NewTableCell("time").SetTextColor(tcell.ColorYellow).SetAlign(tview.AlignLeft))
		table.SetCell(0, 1, tview.NewTableCell("size").SetTextColor(tcell.ColorYellow).SetAlign(tview.AlignLeft))
		table.SetCell(0, 2, tview.NewTableCell("type").SetTextColor(tcell.ColorYellow).SetAlign(tview.AlignLeft))
		if !hiddenText {
			table.SetCell(0, 3, tview.NewTableCell("text").SetTextColor(tcell.ColorYellow).SetAlign(tview.AlignLeft))
		}
		table.SetCell(0, 4, tview.NewTableCell("progress").SetTextColor(tcell.ColorYellow).SetAlign(tview.AlignLeft))

		row := 1

		// active file transfers first
		for _, fp := range fileTransfers {
			table.SetCell(row, 0, tview.NewTableCell(time.Now().Format("15:04:05")))
			table.SetCell(row, 1, tview.NewTableCell(humanReadableSize(fp.Total)))
			dirStr := "file->"
			if fp.Direction == "recv" {
				dirStr = "file<-"
			}
			table.SetCell(row, 2, tview.NewTableCell(dirStr))
			if !hiddenText {
				text := stringutil.LimitStringLen(fp.FileName, 50)
				table.SetCell(row, 3, tview.NewTableCell(text))
			}
			progText := progressBar(fp.Sent, fp.Total)
			if fp.Done {
				if fp.Err != "" {
					progText = "error"
				} else {
					progText = "done"
				}
			}
			table.SetCell(row, 4, tview.NewTableCell(progText))
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
					if fp.Done {
						delete(fileTransfers, key)
					} else {
						fileTransfers[key] = fp
					}
					rebuild()
				})
			}
		}
	}()

	table.SetBorder(true).SetTitle("clipboards")

	return table
}
