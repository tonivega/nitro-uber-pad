package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// docTabs is a lightweight document tab control. Unlike container.DocTabs it
// lets a middle (tertiary) click on a tab request closing that document, and
// only the selected document is part of the widget tree.
type docTabs struct {
	widget.BaseWidget
	Items          []*container.TabItem
	CloseIntercept func(*container.TabItem)
	OnSelected     func(*container.TabItem)
	sel            *container.TabItem
	bar, body      *fyne.Container
	root           fyne.CanvasObject
}

func newDocTabs() *docTabs {
	t := &docTabs{bar: container.NewHBox(), body: container.NewStack()}
	t.root = container.NewBorder(container.NewHScroll(t.bar), nil, nil, nil, t.body)
	t.ExtendBaseWidget(t)
	return t
}

func (t *docTabs) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(t.root) }
func (t *docTabs) Selected() *container.TabItem         { return t.sel }

func (t *docTabs) Append(it *container.TabItem) {
	t.Items = append(t.Items, it)
	t.rebuild()
}

func (t *docTabs) Select(it *container.TabItem) {
	t.sel = it
	t.rebuild()
	if t.OnSelected != nil {
		t.OnSelected(it)
	}
}

func (t *docTabs) Remove(it *container.TabItem) {
	for i, x := range t.Items {
		if x != it {
			continue
		}
		t.Items = append(t.Items[:i], t.Items[i+1:]...)
		if t.sel == it {
			t.sel = nil
			if len(t.Items) > 0 {
				t.Select(t.Items[min(i, len(t.Items)-1)])
				return
			}
		}
		break
	}
	t.rebuild()
}

func (t *docTabs) Refresh() {
	t.rebuild()
	t.BaseWidget.Refresh()
}

func (t *docTabs) rebuild() {
	objs := make([]fyne.CanvasObject, 0, len(t.Items))
	for _, it := range t.Items {
		objs = append(objs, newTabButton(t, it))
	}
	t.bar.Objects = objs
	t.bar.Refresh()
	if t.sel != nil {
		t.body.Objects = []fyne.CanvasObject{t.sel.Content}
	} else {
		t.body.Objects = nil
	}
	t.body.Refresh()
}

func (t *docTabs) close(it *container.TabItem) {
	if t.CloseIntercept != nil {
		t.CloseIntercept(it)
	} else {
		t.Remove(it)
	}
}

type tabButton struct {
	widget.BaseWidget
	t    *docTabs
	item *container.TabItem
	obj  fyne.CanvasObject
}

func newTabButton(t *docTabs, it *container.TabItem) *tabButton {
	b := &tabButton{t: t, item: it}
	selected := t.sel == it
	bg := canvas.NewRectangle(color.Transparent)
	line := canvas.NewRectangle(color.Transparent)
	line.SetMinSize(fyne.NewSize(1, 2))
	if selected {
		bg.FillColor = theme.SelectionColor()
		line.FillColor = theme.PrimaryColor()
	}
	label := canvas.NewText(it.Text, theme.ForegroundColor())
	label.TextStyle.Bold = selected
	closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { t.close(it) })
	closeBtn.Importance = widget.LowImportance
	b.obj = container.NewStack(bg, container.NewBorder(nil, line, nil, nil,
		container.NewHBox(container.NewPadded(label), closeBtn)))
	b.ExtendBaseWidget(b)
	return b
}

func (b *tabButton) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(b.obj) }
func (b *tabButton) Tapped(*fyne.PointEvent)             { b.t.Select(b.item) }

// MouseDown closes the tab on a middle (tertiary) click.
func (b *tabButton) MouseDown(ev *desktop.MouseEvent) {
	if ev.Button == desktop.MouseButtonTertiary {
		b.t.close(b.item)
	}
}
func (b *tabButton) MouseUp(*desktop.MouseEvent) {}
