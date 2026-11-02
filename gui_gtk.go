//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"

	"screenstudio-re/core"
)

const (
	projectCardW  = 240
	projectThumbH = 150
)

// THESIS: Recent work is recognizable media, not rows in a control panel.
// OWN-WORLD: Native dark desktop surfaces, restrained blue accent, image-led project cards.
// STORY: Scan recent recordings, start a capture, open one, play/scrub, then export.
// FIRST VIEWPORT: Calm toolbar above a spacious 16:10 thumbnail grid; recording is top-right.
// FORM: Native project library, replacing the rejected three-column editor scaffold.
// FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, and DESIGN.md
type ui struct {
	root string

	window *gtk.Window
	stack  *gtk.Stack
	flow   *gtk.FlowBox
	scroll *gtk.ScrolledWindow
	search *gtk.SearchEntry
	empty  *gtk.Box

	projects []core.Project
	visible  []core.Project
	player   *videoPlayer
	playName *gtk.Label
	playInfo *gtk.Label
	status   *gtk.Label
	export   *gtk.Button

	rec       *core.Recorder
	starting  bool
	recording bool
	exporting bool
	current   core.Project
}

func runGUI(root string) error {
	gtk.Init(nil)
	settings, _ := gtk.SettingsGetDefault()
	_ = settings.SetProperty("gtk-application-prefer-dark-theme", true)
	if err := style(); err != nil {
		return err
	}

	u := &ui{root: root}
	w, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		return err
	}
	u.window = w
	w.SetTitle("ScreenStudio RE")
	w.SetDefaultSize(1180, 780)
	w.SetSizeRequest(900, 650)
	w.SetPosition(gtk.WIN_POS_CENTER)
	w.Connect("destroy", func() {
		if u.player != nil {
			u.player.stop()
		}
		gtk.MainQuit()
	})

	u.stack, _ = gtk.StackNew()
	u.stack.SetTransitionType(gtk.STACK_TRANSITION_TYPE_CROSSFADE)
	u.stack.SetTransitionDuration(140)
	u.stack.AddNamed(u.browserPage(), "browser")
	u.stack.AddNamed(u.playerPage(), "player")
	u.stack.SetVisibleChildName("browser")
	w.Add(u.stack)

	u.refreshProjects()
	w.ShowAll()
	gtk.Main()
	return nil
}

func (u *ui) browserPage() *gtk.Box {
	page, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	addClass(page, "app-page")

	bar, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 12)
	bar.SetMarginTop(22)
	bar.SetMarginBottom(18)
	bar.SetMarginStart(28)
	bar.SetMarginEnd(28)

	head, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
	title, _ := gtk.LabelNew("Recent projects")
	title.SetHAlign(gtk.ALIGN_START)
	addClass(title, "page-title")
	sub, _ := gtk.LabelNew("Recordings and exports saved on this computer")
	sub.SetHAlign(gtk.ALIGN_START)
	addClass(sub, "secondary")
	head.PackStart(title, false, false, 0)
	head.PackStart(sub, false, false, 0)
	bar.PackStart(head, true, true, 0)

	u.search, _ = gtk.SearchEntryNew()
	u.search.SetPlaceholderText("Search projects")
	u.search.SetSizeRequest(230, -1)
	u.search.Connect("search-changed", u.applySearch)
	bar.PackStart(u.search, false, false, 0)

	openFolder, _ := iconButton("folder-open-symbolic", "Open recordings folder")
	openFolder.Connect("clicked", func() { openFile(u.root) })
	bar.PackStart(openFolder, false, false, 0)

	newRecording, _ := labeledIconButton("media-record-symbolic", "New recording")
	addClass(newRecording, "suggested-action")
	newRecording.Connect("clicked", func() { u.toggleRecord(newRecording) })
	bar.PackStart(newRecording, false, false, 0)
	page.PackStart(bar, false, false, 0)

	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	page.PackStart(sep, false, false, 0)

	u.scroll, _ = gtk.ScrolledWindowNew(nil, nil)
	u.scroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	addClass(u.scroll, "project-scroll")
	page.PackStart(u.scroll, true, true, 0)

	u.empty, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 10)
	u.empty.SetHAlign(gtk.ALIGN_CENTER)
	u.empty.SetVAlign(gtk.ALIGN_CENTER)
	emptyIcon, _ := gtk.ImageNewFromIconName("video-x-generic-symbolic", gtk.ICON_SIZE_DIALOG)
	addClass(emptyIcon, "empty-icon")
	emptyTitle, _ := gtk.LabelNew("No recordings yet")
	addClass(emptyTitle, "empty-title")
	emptyCopy, _ := gtk.LabelNew("Start a recording and it will appear here.")
	addClass(emptyCopy, "secondary")
	u.empty.PackStart(emptyIcon, false, false, 0)
	u.empty.PackStart(emptyTitle, false, false, 0)
	u.empty.PackStart(emptyCopy, false, false, 0)
	return page
}

func (u *ui) playerPage() *gtk.Box {
	page, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	addClass(page, "player-page")

	bar, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 10)
	bar.SetMarginTop(12)
	bar.SetMarginBottom(12)
	bar.SetMarginStart(16)
	bar.SetMarginEnd(16)
	back, _ := iconButton("go-previous-symbolic", "Back to projects")
	back.Connect("clicked", func() {
		u.player.stop()
		u.stack.SetVisibleChildName("browser")
		u.window.SetTitle("ScreenStudio RE")
	})
	bar.PackStart(back, false, false, 0)

	names, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 1)
	u.playName, _ = gtk.LabelNew("Project")
	u.playName.SetHAlign(gtk.ALIGN_START)
	addClass(u.playName, "project-title")
	u.playInfo, _ = gtk.LabelNew("")
	u.playInfo.SetHAlign(gtk.ALIGN_START)
	addClass(u.playInfo, "secondary")
	names.PackStart(u.playName, false, false, 0)
	names.PackStart(u.playInfo, false, false, 0)
	bar.PackStart(names, true, true, 0)

	u.status, _ = gtk.LabelNew("")
	addClass(u.status, "secondary")
	bar.PackStart(u.status, false, false, 0)

	openExportFolder, _ := labeledIconButton("folder-open-symbolic", "Export folder")
	openExportFolder.SetTooltipText("Open the folder containing the exported video")
	openExportFolder.Connect("clicked", func() {
		if u.current.Dir != "" {
			openFile(u.current.Dir)
		}
	})
	bar.PackStart(openExportFolder, false, false, 0)

	openOutput, _ := iconButton("document-open-symbolic", "Open exported video")
	openOutput.Connect("clicked", func() {
		path := filepath.Join(u.current.Dir, "output.mp4")
		if _, err := os.Stat(path); err == nil {
			openFile(path)
		} else {
			u.status.SetText("Export this project first")
		}
	})
	bar.PackStart(openOutput, false, false, 0)

	u.export, _ = labeledIconButton("document-save-symbolic", "Export MP4")
	addClass(u.export, "suggested-action")
	u.export.Connect("clicked", u.doExport)
	bar.PackStart(u.export, false, false, 0)
	page.PackStart(bar, false, false, 0)
	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	page.PackStart(sep, false, false, 0)

	u.player, _ = newVideoPlayer()
	page.PackStart(u.player.widget, true, true, 0)
	return page
}

func labeledIconButton(icon, label string) (*gtk.Button, error) {
	b, err := gtk.ButtonNewWithLabel(label)
	if err != nil {
		return nil, err
	}
	img, _ := gtk.ImageNewFromIconName(icon, gtk.ICON_SIZE_BUTTON)
	b.SetImage(img)
	b.SetAlwaysShowImage(true)
	return b, nil
}

func (u *ui) refreshProjects() {
	go func() {
		projects, err := core.Projects(u.root)
		if err != nil && !os.IsNotExist(err) {
			return
		}
		for _, p := range projects {
			_ = core.EnsureThumbnail(p)
		}
		glib.IdleAdd(func() {
			u.projects = projects
			u.applySearch()
			if os.Getenv("SCREENSTUDIO_QA_OPEN_FIRST") == "1" && len(projects) > 0 {
				u.openProject(projects[0])
			}
		})
	}()
}

func (u *ui) applySearch() {
	text, _ := u.search.GetText()
	term := strings.ToLower(strings.TrimSpace(text))
	u.visible = u.visible[:0]
	for _, p := range u.projects {
		if term == "" || strings.Contains(strings.ToLower(p.Name), term) {
			u.visible = append(u.visible, p)
		}
	}
	u.rebuildGrid()
}

func (u *ui) rebuildGrid() {
	if child, err := u.scroll.GetChild(); err == nil && child != nil {
		u.scroll.Remove(child)
	}
	if len(u.visible) == 0 {
		u.scroll.Add(u.empty)
		u.empty.ShowAll()
		return
	}
	flow, _ := gtk.FlowBoxNew()
	u.flow = flow
	flow.SetSelectionMode(gtk.SELECTION_NONE)
	flow.SetActivateOnSingleClick(true)
	flow.SetHomogeneous(true)
	flow.SetMinChildrenPerLine(1)
	flow.SetMaxChildrenPerLine(3)
	flow.SetColumnSpacing(18)
	flow.SetRowSpacing(18)
	flow.SetHAlign(gtk.ALIGN_CENTER)
	flow.SetVAlign(gtk.ALIGN_START)
	flow.SetMarginTop(20)
	flow.SetMarginBottom(20)
	flow.SetMarginStart(28)
	flow.SetMarginEnd(28)
	flow.Connect("child-activated", func(_ *gtk.FlowBox, child *gtk.FlowBoxChild) {
		i := child.GetIndex()
		if i >= 0 && i < len(u.visible) {
			u.openProject(u.visible[i])
		}
	})
	for _, p := range u.visible {
		flow.Insert(u.projectCard(p), -1)
	}
	u.scroll.Add(flow)
	flow.ShowAll()
}

func (u *ui) projectCard(p core.Project) *gtk.Box {
	card, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 8)
	card.SetSizeRequest(projectCardW, -1)
	addClass(card, "project-card")

	frame, _ := gtk.FrameNew("")
	frame.SetShadowType(gtk.SHADOW_NONE)
	frame.SetSizeRequest(projectCardW, projectThumbH)
	addClass(frame, "thumbnail")
	if pix, err := gdk.PixbufNewFromFileAtScale(p.Thumbnail, projectCardW, projectThumbH, false); err == nil {
		img, _ := gtk.ImageNewFromPixbuf(pix)
		frame.Add(img)
	} else {
		placeholder, _ := gtk.ImageNewFromIconName("video-x-generic-symbolic", gtk.ICON_SIZE_DIALOG)
		frame.Add(placeholder)
	}
	card.PackStart(frame, false, false, 0)

	name, _ := gtk.LabelNew(projectDisplayName(p.Name))
	name.SetHAlign(gtk.ALIGN_START)
	name.SetEllipsize(3)
	addClass(name, "card-title")
	card.PackStart(name, false, false, 0)
	meta, _ := gtk.LabelNew(projectMeta(p))
	meta.SetHAlign(gtk.ALIGN_START)
	addClass(meta, "card-meta")
	card.PackStart(meta, false, false, 0)
	return card
}

func (u *ui) openProject(p core.Project) {
	u.current = p
	cur, _ := core.LoadCursor(filepath.Join(p.Dir, "cursor.json"))
	var segments []core.Segment
	if len(cur.Samples) > 1 {
		segments = core.BuildTimeline(cur, core.DefaultTimelineOpts(2)).Segments
	}
	u.playName.SetText(projectDisplayName(p.Name))
	u.playInfo.SetText(projectMeta(p))
	u.status.SetText("")
	u.player.setProject(p, segments)
	u.stack.SetVisibleChildName("player")
	u.window.SetTitle(projectDisplayName(p.Name) + " — ScreenStudio RE")
}

func (u *ui) toggleRecord(button *gtk.Button) {
	if u.starting {
		return
	}
	if u.recording {
		u.recording = false
		button.SetSensitive(false)
		button.SetLabel("Finalizing…")
		rc := u.rec
		go func() {
			path, err := rc.Stop()
			if err == nil {
				dir := filepath.Dir(path)
				err = core.ExportAuto(dir, filepath.Join(dir, "output.mp4"), core.ExportOpts{})
			}
			glib.IdleAdd(func() {
				button.SetSensitive(true)
				button.SetLabel("New recording")
				if err != nil {
					showError(u.window, "Could not finish recording", err)
				}
				u.refreshProjects()
			})
		}()
		return
	}
	u.starting = true
	button.SetSensitive(false)
	button.SetLabel("Choose a screen…")
	rc := &core.Recorder{}
	go func() {
		_, err := rc.Record(u.root)
		glib.IdleAdd(func() {
			u.starting = false
			button.SetSensitive(true)
			if err != nil {
				button.SetLabel("New recording")
				showError(u.window, "Could not start recording", err)
				return
			}
			u.rec, u.recording = rc, true
			button.SetLabel("Stop recording")
		})
	}()
}

func (u *ui) doExport() {
	if u.exporting || u.current.Dir == "" {
		return
	}
	u.exporting = true
	u.export.SetSensitive(false)
	u.export.SetLabel("Exporting…")
	u.status.SetText("Rendering MP4")
	p := u.current
	go func() {
		err := core.ExportAuto(p.Dir, filepath.Join(p.Dir, "output.mp4"),
			core.ExportOpts{Canvas: core.W{W: 1920, H: 1080}})
		glib.IdleAdd(func() {
			u.exporting = false
			u.export.SetSensitive(true)
			u.export.SetLabel("Export MP4")
			if err != nil {
				u.status.SetText("Export failed")
				showError(u.window, "Could not export video", err)
				return
			}
			u.status.SetText("Export complete")
			if u.current.Dir == p.Dir {
				p.VideoPath = filepath.Join(p.Dir, "output.mp4")
				p.HasExport = true
				u.current = p
				cur, _ := core.LoadCursor(filepath.Join(p.Dir, "cursor.json"))
				var segments []core.Segment
				if len(cur.Samples) > 1 {
					segments = core.BuildTimeline(cur, core.DefaultTimelineOpts(2)).Segments
				}
				u.player.setProject(p, segments)
			}
			u.refreshProjects()
		})
	}()
}

func showError(parent *gtk.Window, title string, err error) {
	d := gtk.MessageDialogNew(parent, gtk.DIALOG_MODAL, gtk.MESSAGE_ERROR, gtk.BUTTONS_CLOSE, "%s", title)
	d.FormatSecondaryText("%s", err.Error())
	d.Run()
	d.Destroy()
}

func projectDisplayName(name string) string {
	if t, err := time.Parse("2006-01-02_15-04-05", name); err == nil {
		return t.Format("Recording — Jan 2, 15:04")
	}
	return name
}

func projectMeta(p core.Project) string {
	resolution := fmt.Sprintf("%d×%d", p.Width, p.Height)
	if p.Width == 0 {
		resolution = "Video"
	}
	state := "Original"
	if p.HasExport {
		state = "Exported"
	}
	return fmt.Sprintf("%s  ·  %s  ·  %s", formatTime(p.Duration), resolution, state)
}

type styleable interface {
	GetStyleContext() (*gtk.StyleContext, error)
}

func addClass(w styleable, class string) {
	if c, err := w.GetStyleContext(); err == nil {
		c.AddClass(class)
	}
}

func style() error {
	screen, err := gdk.ScreenGetDefault()
	if err != nil {
		return err
	}
	css, err := gtk.CssProviderNew()
	if err != nil {
		return err
	}
	if err := css.LoadFromData(`
		window, .app-page, .player-page { background: #111114; color: #f2f2f4; }
		separator { background: #2b2b31; min-height: 1px; }
		label { color: #f2f2f4; }
		.page-title { font-size: 26px; font-weight: 700; }
		.project-title { font-size: 15px; font-weight: 600; }
		.secondary, .card-meta, .timecode { color: #9b9ba6; }
		.card-title { font-size: 14px; font-weight: 600; }
		.card-meta { font-size: 12px; }
		.project-scroll { background: #111114; }
		flowbox { background: #111114; }
		flowboxchild { padding: 0; border-radius: 10px; background: transparent; }
		flowboxchild:hover .thumbnail { border-color: #5f83d9; }
		.project-card { padding: 3px; }
		.thumbnail { background: #19191e; border: 1px solid #303038; border-radius: 9px; }
		.preview-frame { background: #09090b; border: 1px solid #292930; border-radius: 10px; }
		.empty-icon { color: #777781; }
		.empty-title { font-size: 18px; font-weight: 600; }
		button { min-height: 32px; padding: 0 12px; border-radius: 7px; background: #232329; color: #ededf0; border: 1px solid #34343c; }
		button:hover { background: #2b2b32; border-color: #45454f; }
		button.suggested-action { background: #3f74d8; border-color: #4a7ee0; color: white; }
		button.suggested-action:hover { background: #4b80e5; }
		button.icon-button { min-width: 34px; padding: 0; }
		entry { min-height: 32px; border-radius: 7px; color: #ededf0; background: #1c1c21; border: 1px solid #323239; }
		scale trough { min-height: 5px; border-radius: 3px; background: #303038; }
		scale highlight { background: #5888e8; border-radius: 3px; }
		scale slider { min-width: 14px; min-height: 14px; border-radius: 50%; background: #f3f3f5; }
	`); err != nil {
		return err
	}
	gtk.AddProviderForScreen(screen, css, uint(gtk.STYLE_PROVIDER_PRIORITY_APPLICATION))
	return nil
}
