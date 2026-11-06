package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Session-only bounded journals. HTTP bodies, headers and script source are not
// recorded: logs should be useful without quietly becoming a credentials dump.
type activityEvent struct {
	at time.Time
	category, detail string
}

type eventStore struct {
	mu sync.Mutex
	items []activityEvent
}

func (s *eventStore) add(limit int, category, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) >= limit {
		copy(s.items, s.items[len(s.items)-limit+1:])
		s.items = s.items[:limit-1]
	}
	s.items = append(s.items, activityEvent{time.Now(), category, detail})
}

func (s *eventStore) snapshot() []activityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]activityEvent(nil), s.items...)
}

func (a *App) Log(category, detail string) { a.events.add(2000, category, detail) }

func (a *App) Notify(msg string) {
	a.notices.add(200, "notification", msg)
	a.Log("notification", msg)
	generation := a.noticeGeneration.Add(1)
	runes := []rune(strings.ReplaceAll(msg, "\n", " "))
	preview := msg
	if len(runes) > 65 {
		preview = string(runes[:65]) + "…"
	} else {
		preview = string(runes)
	}
	a.noticeLabel.SetText(preview)
	a.noticeLabel.Importance = widget.HighImportance
	a.noticeLabel.Refresh()
	// A finite blink; a newer notification owns the indicator immediately.
	go func() {
		ticker := time.NewTicker(180 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; i < 5; i++ {
			select {
			case <-a.stopped:
				return
			case <-ticker.C:
			}
			if a.noticeGeneration.Load() != generation {
				return
			}
			a.noticeLabel.Importance = widget.MediumImportance
			if i%2 == 1 {
				a.noticeLabel.Importance = widget.HighImportance
			}
			a.noticeLabel.Refresh()
		}
	}()
}

func (a *App) showNotifications() {
	a.showJournal("Notifications — click an entry to expand", &a.notices)
}

func (a *App) showLogs() { a.showJournal("Activity logs (this session)", &a.events) }

func (a *App) showJournal(title string, store *eventStore) {
	var items []activityEvent
	filter := widget.NewEntry()
	filter.SetPlaceHolder("Filter category or message…")
	list := widget.NewList(func() int { return len(items) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(items) { return }
			ev := items[i]
			label := o.(*widget.Label)
			label.Wrapping = fyne.TextTruncate
			label.SetText(fmt.Sprintf("%s  [%s] %s", ev.at.Format("15:04:05.000"), ev.category, strings.ReplaceAll(ev.detail, "\n", " ")))
		})
	detail := widget.NewMultiLineEntry()
	detail.TextStyle = fyne.TextStyle{Monospace: true}
	detail.Wrapping = fyne.TextWrapWord
	detail.SetMinRowsVisible(5)
	list.OnSelected = func(i widget.ListItemID) {
		if i >= len(items) { return }
		ev := items[i]
		detail.SetText(ev.at.Format(time.RFC3339Nano) + "  [" + ev.category + "]\n\n" + ev.detail)
	}
	refresh := func() {
		all := store.snapshot()
		items = nil
		q := strings.ToLower(filter.Text)
		for i := len(all)-1; i >= 0; i-- {
			ev := all[i]
			if strings.Contains(strings.ToLower(ev.category+" "+ev.detail), q) {
				items = append(items, ev)
			}
		}
		list.UnselectAll()
		list.Refresh()
		if len(items) > 0 { list.Select(0) } else { detail.SetText("No matching entries") }
	}
	filter.OnChanged = func(string) { refresh() }
	refresh()
	buttons := container.NewHBox(widget.NewButton("Refresh", refresh), widget.NewButton("Copy visible history", func() {
		var b strings.Builder
		for _, ev := range items {
			fmt.Fprintf(&b, "%s [%s] %s\n", ev.at.Format(time.RFC3339Nano), ev.category, ev.detail)
		}
		a.win.Clipboard().SetContent(b.String())
	}))
	split := container.NewVSplit(list, detail)
	split.Offset = 0.6
	a.showWindow("journal:"+title, title, container.NewBorder(container.NewVBox(filter, buttons), nil, nil, nil, split), fyne.NewSize(900, 620))
}
