//go:build windows

package main

import (
	"os"
	"path/filepath"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"screenstudio-re/core"
)

type winUI struct {
	root      string
	rec       *core.Recorder
	recording bool
	cur       string
	exporting bool
	names     []string

	mw     *walk.MainWindow
	list   *walk.ListBox
	status *walk.Label
	btnRec *walk.PushButton
	btnExp *walk.PushButton
	open   *walk.ComboBox
}

func runGUI(root string) error {
	u := &winUI{root: root}
	if err := buildWindow(u); err != nil {
		return err
	}
	u.refresh()
	u.mw.Run()
	return nil
}

func buildWindow(u *winUI) error {
	return MainWindow{
		AssignTo: &u.mw,
		Title:    "ScreenStudio RE",
		MinSize:  Size{Width: 760, Height: 460},
		Size:     Size{Width: 900, Height: 540},
		Layout:   HBox{MarginsZero: false, Spacing: 10},
		Children: []Widget{
			ListBox{
				AssignTo: &u.list,
				OnCurrentIndexChanged: func() {
					i := u.list.CurrentIndex()
					if i < 0 || i >= len(u.names) {
						return
					}
					u.cur = u.names[i]
					u.status.SetText(u.cur + " selected — Export MP4 when ready.")
				},
			},
			Composite{
				Layout: VBox{},
				Children: []Widget{
					PushButton{AssignTo: &u.btnRec, Text: "● Record", OnClicked: u.toggleRecord},
					Label{AssignTo: &u.status, Text: "Idle"},
					GroupBox{Title: "Export", Layout: VBox{}, Children: []Widget{
						ComboBox{AssignTo: &u.open, Model: []string{"1920x1080", "1600x1000", "1280x800"}, Value: "1600x1000"},
						PushButton{AssignTo: &u.btnExp, Text: "Export MP4", OnClicked: u.doExport},
						PushButton{Text: "Open output", OnClicked: u.openOutput},
						PushButton{Text: "Open export folder", OnClicked: u.openExportFolder},
					}},
				},
			},
		},
	}.Create()
}

func (u *winUI) refresh() {
	names, _ := core.Sessions(u.root)
	u.names = names
	u.list.SetModel(names)
}

func (u *winUI) toggleRecord() {
	if u.recording {
		rc := u.rec
		u.recording = false
		u.btnRec.SetText("● Record")
		u.status.SetText("Finalizing…")
		go func() {
			path, err := rc.Stop()
			if err == nil {
				dir := filepath.Dir(path)
				err = core.ExportAuto(dir, filepath.Join(dir, "output.mp4"), core.ExportOpts{})
			}
			if err != nil {
				u.mw.Synchronize(func() {
					walk.MsgBox(u.mw, "Stop", err.Error(), walk.MsgBoxIconError)
				})
			}
			u.mw.Synchronize(func() {
				u.refresh()
				u.status.SetText("Recording stopped — auto-zoom ready.")
			})
		}()
		return
	}
	u.refresh()
	rc := &core.Recorder{}
	sess, err := rc.Record(u.root)
	if err != nil {
		walk.MsgBox(u.mw, "Record", err.Error(), walk.MsgBoxIconError)
		return
	}
	_ = sess
	u.rec, u.recording = rc, true
	u.btnRec.SetText("■ Stop")
	u.status.SetText("Recording…")
}

func (u *winUI) doExport() {
	name := u.cur
	if name == "" || u.exporting {
		u.status.SetText("pick a session first")
		return
	}
	dir := filepath.Join(u.root, name)
	cw, ch := parseWxH(u.open.Text())
	u.exporting = true
	u.btnExp.SetEnabled(false)
	u.status.SetText("Rendering " + name + "…")
	go func() {
		defer func() {
			u.mw.Synchronize(func() {
				u.exporting = false
				u.btnExp.SetEnabled(true)
			})
		}()
		if err := core.ExportAuto(dir, filepath.Join(dir, "output.mp4"), core.ExportOpts{Canvas: core.W{W: cw, H: ch}}); err != nil {
			u.mw.Synchronize(func() {
				u.status.SetText(err.Error())
				walk.MsgBox(u.mw, "Export", err.Error(), walk.MsgBoxIconError)
			})
			return
		}
		u.mw.Synchronize(func() {
			u.status.SetText("Exported " + name)
			openOutput(u.root, name)
		})
	}()
}

func (u *winUI) openOutput() {
	if u.cur == "" {
		return
	}
	openOutput(u.root, u.cur)
}

func (u *winUI) openExportFolder() {
	dir := u.root
	if u.cur != "" {
		dir = filepath.Join(dir, u.cur)
	}
	openFile(dir)
}

func openOutput(root, name string) {
	p := filepath.Join(root, name, "output.mp4")
	if _, err := os.Stat(p); err == nil {
		openFile(p)
	}
}
