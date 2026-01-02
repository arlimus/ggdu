// Copyright 2026 Christian Dominik Richter
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var logFile = ""

type LOG_LEVEL byte

const (
	DEBUG LOG_LEVEL = iota
	INFO
	ERROR
)

func init() {
	// // uncomment to log everything
	// logFile = ".ggdu.log"

	if logFile != "" {
		file, err := os.Create(logFile)
		if err != nil {
			panic("cannot write logs: " + err.Error())
		}
		file.Close()
	}
}

type db struct {
	root *Folder
}

type Folder struct {
	ID         string
	Name       string
	Folders    []*Folder
	Files      []*File
	Date       int64
	LastUpdate int64

	// aggregate info, computed on the fly
	size      int64
	known     int // aggregate known folders at this level
	unknown   int // aggregate unknown folders at this level
	folderIdx map[string]*Folder
	fileIdx   map[string]*File
	path      string  // full path
	parent    *Folder // two-way navigation
	save      func() error
	lastIdx   int
}

type File struct {
	ID   string
	Name string
	Ext  string
	Size int // in bytes
	Date int64
}

var refreshDelay = 24 * time.Hour
var tooOld = (time.Now().Add(-refreshDelay)).Unix()

var log = func(msg string, level LOG_LEVEL) {
	fmt.Println(msg)
}

const savePath = "db.json"

func main() {
	var data *Folder
	var err error
	if fileExists(savePath) {
		data, err = load(savePath)
		if err != nil {
			panic(err)
		}
	} else {
		data = &Folder{}
	}
	data.path = "/"

	startApp(data)
}

func startApp(root *Folder) {
	app := tview.NewApplication()

	debug := tview.NewTextView().SetTextAlign(tview.AlignLeft)
	debugTxt := []string{}
	debugMsg := func(msg string, level LOG_LEVEL) {
		if logFile != "" {
			f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err == nil {
				f.WriteString(msg)
				f.Write([]byte{'\n'})
				f.Close()
			}
		}

		if level < INFO {
			return
		}
		debugTxt = append(debugTxt, msg)
		if len(debugTxt) > 3 {
			debugTxt = debugTxt[len(debugTxt)-3:]
		}
		debug.SetText(strings.Join(debugTxt, "\n"))
	}
	log = debugMsg
	debugMsg("Keys: l = load the folder, x = recursively load everything in a folder, F5 = refresh", INFO)
	debugMsg("Temporary cache is stored in: "+savePath, INFO)
	debugMsg("By default fetch data only every "+refreshDelay.String()+" (override with f+l or f+x)", INFO)

	root.save = func() error {
		return save(root)
	}
	root.ensureData(false, nil)
	// we picked one field that must definitely not be nil after a successful refresh, which might have happened
	if root.folderIdx == nil {
		root.rebuild()
	}

	curFolder := root
	var listItems []*Folder

	list := tview.NewList().ShowSecondaryText(false)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'k':
			return tcell.NewEventKey(tcell.KeyUp, ' ', tcell.ModNone)
		case 'j':
			return tcell.NewEventKey(tcell.KeyDown, ' ', tcell.ModNone)
		}
		return event
	})

	header := tview.NewTextView().
		SetTextAlign(tview.AlignLeft)

	grid := tview.NewGrid().
		SetRows(1, 0, 3).
		SetColumns(0).
		AddItem(header, 0, 0, 1, 1, 0, 0, false).
		AddItem(list, 1, 0, 1, 1, 0, 0, true).
		AddItem(debug, 2, 0, 1, 1, 0, 0, false)

	// box := tview.NewGrid().SetBorder(true).SetTitle("Explore " + f.path)
	// box.Set

	var selectFn func(*Folder)
	selectFn = func(f *Folder) {
		curFolder = f
		listItems = f.explorer(list, selectFn)
		header.SetText("--- " + f.path + " (" + formatSize(f.size) + ") ---")
		// debugMsg("rendered " + f.path)
	}

	var forceMode = false

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			app.Stop()
			return nil // stop propagation

		// case tcell.KeyF5:
		// 	go func() {
		// 		curFolder.ensureData(true, nil)
		// 		selectFn(curFolder)
		// 		app.Draw()
		// 	}()

		case tcell.KeyRune:
			ch := event.Rune()
			if ch == 'q' {
				app.Stop()
				return nil
			}

			if ch == 'l' || ch == 'x' {
				i := list.GetCurrentItem()
				if i >= len(listItems) {
					return nil
				}
				folder := listItems[i]
				if folder == nil {
					return nil
				}

				var deep *goDeep
				if ch == 'x' {
					deep = &goDeep{max: 1, cur: 0, onUpdate: func(cur *Folder) {
						for ; cur != nil; cur = cur.parent {
							if cur == curFolder {
								selectFn(cur)
								break
							}
						}
						app.Draw()
					}}
				}

				go func() {
					msg := "load " + folder.path
					if forceMode {
						msg += " (force refresh)"
					}
					log(msg, INFO)

					folder.ensureData(forceMode, deep)

					if deep != nil {
						log("all done for "+folder.path, INFO)
					}

					selectFn(curFolder)
					app.Draw()
				}()

				return nil
			}

			if ch == 'f' {
				forceMode = true
			} else {
				forceMode = false
			}

		}
		return event // Continue processing other events
	})

	selectFn(curFolder)
	app.SetRoot(grid, true).SetFocus(list)

	if err := app.Run(); err != nil {
		fmt.Println(err)
	}
}

const delim = "^^^^^"

var gdriveListHeader = strings.Join([]string{"Id", "Name", "Type", "Size", "Created"}, delim)

func save(root *Folder) error {
	res, err := json.Marshal(root)
	if err != nil {
		return err
	}

	return os.WriteFile(savePath, res, 0644)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	// The file may or may not exist if a different error occurred (e.g., permission error).
	// For a simple existence check, we treat any error other than os.ErrNotExist
	// as an indication that something is there (or at least, the path is valid).
	// A more robust application would handle other errors specifically.
	return err == nil
}

func load(path string) (*Folder, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	res := Folder{}
	err = json.Unmarshal(raw, &res)

	all := []*Folder{&res}
	for i := 0; i < len(all); i++ {
		cur := all[i]
		all = append(all, cur.Folders...)
		cur.save = func() error {
			return save(&res)
		}
	}
	res.path = "/"
	res.rebuild()

	return &res, err
}

const MAX_COUNT = 500

func (f *Folder) getFiles() error {
	cmd := []string{"gdrive", "files", "list", "--field-separator", delim, "--max", strconv.Itoa(MAX_COUNT)}
	if f.ID != "" {
		cmd = append(cmd, "--parent", f.ID)
	}

	raw, err := sh(cmd...)
	if err != nil {
		return err
	}

	lines := strings.Split(string(raw), "\n")
	header := lines[0]
	if header != gdriveListHeader {
		return errors.New("Unexpected header in gdrive list: " + header)
	}

	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}

		parts := strings.Split(line, delim)
		switch parts[2] {
		case "regular":
			f.Files = append(f.Files, &File{
				ID:   parts[0],
				Name: parts[1],
				Ext:  filepath.Ext(parts[1]),
				Size: parseSize(parts[3]),
				Date: parseDate(parts[4]),
			})

		case "folder":
			f.Folders = append(f.Folders, &Folder{
				ID:   parts[0],
				Name: parts[1],
				Date: parseDate(parts[4]),
				save: f.save,
			})

		case "document":
			// ignore it
		case "shortcut":
			// ignore that too

		default:
			panic("unknown type of file: " + parts[2])
		}
	}

	f.LastUpdate = time.Now().Unix()

	return nil
}

func sh(parts ...string) (string, error) {
	log("sh> "+strings.Join(parts, " "), DEBUG)
	cmd := exec.Command(parts[0], parts[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", errors.New("failed to start command: " + err.Error())
	}

	if stderr.Len() > 0 {
		log("ERROR: "+stderr.String(), ERROR)
	}

	return stdout.String(), nil
}

func parseSize(s string) int {
	parts := strings.Split(s, " ")
	if len(parts) == 1 {
		res, err := strconv.Atoi(s)
		if err != nil {
			panic("Failed to parse as size: " + s)
		}
		return res
	}

	if len(parts) != 2 {
		panic("Failed to parse size: " + s)
	}

	res, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		panic("Failed to parse as size: " + s)
	}
	switch strings.ToLower(parts[1]) {
	case "b":
		return int(res)
	case "kb":
		return int(res * 1024)
	case "mb":
		return int(res * 1024 * 1024)
	case "gb":
		return int(res * 1024 * 1024 * 1024)
	case "tb":
		return int(res * 1024 * 1024 * 1024 * 1024)
	}
	panic("Failed to parse as size: " + s)
}

func formatSize(i int64) string {
	if i == 0 {
		return ""
	}

	if i < 1024 {
		return fmt.Sprintf("%d", i) + "b"
	}

	f := float32(i / 1024)
	if f < 1024 {
		return fmt.Sprintf("%.1fkb", f)
	}

	f = f / 1024
	if f < 1024 {
		return fmt.Sprintf("%.1fmb", f)
	}

	f = f / 1024
	if f < 1024 {
		return fmt.Sprintf("%.1fgb", f)
	}

	f = f / 1024
	return fmt.Sprintf("%.1ftb", f)
}

func parseDate(s string) int64 {
	time, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		panic("Failed to parse as time: " + s)
	}
	return time.Unix()
}

func (f *Folder) rebuild() {
	f.size = 0
	f.folderIdx = map[string]*Folder{}
	f.fileIdx = map[string]*File{}
	f.unknown = 0
	f.known = 0

	for i := range f.Folders {
		folder := f.Folders[i]
		f.attachChild(folder)
		folder.rebuild()
		f.folderIdx[folder.Name] = folder
		f.size += folder.size
		if folder.LastUpdate < tooOld {
			f.unknown += 1
		} else {
			f.known += 1
		}
	}

	for i := range f.Files {
		file := f.Files[i]
		f.size += int64(file.Size)
	}
}

type goDeep struct {
	max      int
	cur      int
	onUpdate func(f *Folder)
}

func (f *Folder) ensureData(forceUpdate bool, goDeep *goDeep) {
	if !forceUpdate && f.LastUpdate > tooOld {
		return
	}

	oldSize := f.size

	if err := f.getFiles(); err != nil {
		panic(err)
	}
	if f.save == nil {
		panic("Reached a folder without a save function: " + f.path)
	}
	if err := f.save(); err != nil {
		panic(err)
	}

	if goDeep != nil {
		goDeep.max += len(f.Folders)

		for i := range f.Folders {
			folder := f.Folders[i]
			f.attachChild(folder)
			folder.ensureData(forceUpdate, goDeep)
			f.rebuild()
			goDeep.onUpdate(f)
		}
		// otherwise it never gets rebuilt vv
		if len(f.Folders) == 0 {
			f.rebuild()
		}

		goDeep.cur += 1
		if f.path == "" {
			log(fmt.Sprintf("empty path on entry: %#v", f), DEBUG)
		}
		log(fmt.Sprintf("progress: %s %d/%d %s", progressbar(float64(goDeep.cur)/float64(goDeep.max), 30), goDeep.cur, goDeep.max, f.path), INFO)

	} else {
		log("rebuilding idx...", DEBUG)
		f.rebuild()
	}

	sizeChange := (f.size - oldSize)
	for parent := f.parent; parent != nil; parent = parent.parent {
		parent.size += sizeChange
		parent.unknown -= 1
		parent.known += 1
	}
}

func (f *Folder) attachChild(child *Folder) {
	child.parent = f
	child.path = filepath.Join(f.path, f.Name)
}

func (f *Folder) explorer(list *tview.List, selectFn func(*Folder)) []*Folder {
	list.Clear()

	// we need a copy so we can sort it without breaking
	res := make([]*Folder, len(f.Folders))
	copy(res, f.Folders)

	sort.Slice(res, func(i, j int) bool {
		a := res[i]
		b := res[j]
		if a.size != b.size {
			return a.size > b.size
		}
		return res[i].Name < res[j].Name
	})

	offset := 1
	if f.parent != nil {
		list.AddItem(fmt.Sprintf("%+8s %s [blue]%s", "", "", ".."),
			"", 0, func() {
				f.lastIdx = list.GetCurrentItem()
				selectFn(f.parent)
			})
		offset += 1
	}

	for i := range res {
		folder := res[i]
		var progress float64
		if f.size >= 1 {
			progress = float64(folder.size) / float64(f.size)
		}
		list.AddItem(fmt.Sprintf("[orange::b]%+8s [white]%10s [blue::b]%s", formatSize(folder.size), progressbar(progress, 10), folder.Name+"/"),
			"", 0, func() {
				f.lastIdx = list.GetCurrentItem()
				selectFn(folder)
			})
		res = append(res, folder)
		// list.SetCellSimple(i+offset, 0, formatSize(folder.size))
		// list.SetCellSimple(i+offset, 1, progressbar(progress, 10))
		// list.SetCell(i+offset, 2, tview.NewTableCell(folder.Name).SetTextColor(tcell.ColorBlue))
	}
	offset += len(res)

	files := make([]*File, len(f.Files))
	copy(files, f.Files)
	sort.Slice(files, func(i, j int) bool {
		a := files[i]
		b := files[j]
		if a.Size != b.Size {
			return a.Size > b.Size
		}
		return files[i].Name < files[j].Name
	})

	for i := range files {
		file := files[i]
		var progress float64
		if f.size >= 1 {
			progress = float64(file.Size) / float64(f.size)
		}
		list.AddItem(fmt.Sprintf("[orange::b]%+8s [white]%10s %s", formatSize(int64(file.Size)), progressbar(progress, 10), file.Name),
			"", 0, nil)
		// list.SetCellSimple(i+offset, 0, formatSize(int64(file.Size)))
		// list.SetCellSimple(i+offset, 1, progressbar(progress, 10))
		// list.SetCell(i+offset, 2, tview.NewTableCell(file.Name).SetTextColor(tcell.ColorBlue))
	}

	max := list.GetItemCount()
	last := f.lastIdx
	if max < last {
		last = max - 1
	}
	list.SetCurrentItem(last)

	return res
}

var progressRunes = []rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉', '█'}

// progress: 0 - 1.0 (100%)
// width: number of characters
func progressbar(progress float64, width int) string {
	var segPct = 1 / float64(width)
	var full = int(math.Floor(progress / segPct))
	var i = 0

	res := make([]rune, width)
	for ; i < full && i < width; i++ {
		res[i] = progressRunes[len(progressRunes)-1]
	}
	if i >= width {
		return string(res)
	}

	rem := progress - float64(full)*segPct
	idx := int(math.Round(
		rem / segPct * float64(len(progressRunes)-1),
	))
	if idx >= len(progressRunes) {
		panic(fmt.Sprintf("trying to access a rune that's above max: rem=%f segPct=%f and idx=%d", rem, segPct, idx))
	}
	res[i] = progressRunes[idx]
	i++

	for ; i < width; i++ {
		res[i] = ' '
	}

	return string(res)
}
