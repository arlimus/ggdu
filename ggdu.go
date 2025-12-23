package main

import (
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
	path      string
}

type File struct {
	ID   string
	Name string
	Ext  string
	Size int // in bytes
	Date int64
}

var tooOld = (time.Now().Add(-7 * 24 * time.Hour)).Unix()

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

	if data.LastUpdate < tooOld {
		if err := data.getFiles(); err != nil {
			panic(err)
		}
		if err = data.save(); err != nil {
			panic(err)
		}
	}

	data.rebuild("/")
	data.explorer()
}

const delim = "^^^^^"

var gdriveListHeader = strings.Join([]string{"Id", "Name", "Type", "Size", "Created"}, delim)

func (f *Folder) save() error {
	res, err := json.Marshal(f)
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
	return &res, err
}

func (f *Folder) getFiles() error {
	cmd := []string{"gdrive", "files", "list", "--field-separator", delim}
	if f.ID != "" {
		cmd = append(cmd, "--query", "'"+f.ID+"' in parents")
	}

	raw, err := sh(cmd...).Output()
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
			})

		case "document":
			fmt.Println("\033[37m... ignore " + parts[1] + "\033[0m")

		default:
			panic("unknown type of file: " + parts[2])
		}
	}

	f.LastUpdate = time.Now().Unix()

	return nil
}

func sh(parts ...string) *exec.Cmd {
	fmt.Println("--- " + strings.Join(parts, " "))
	return exec.Command(parts[0], parts[1:]...)
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

func (f *Folder) rebuild(curPath string) {
	f.size = 0
	f.folderIdx = map[string]*Folder{}
	f.fileIdx = map[string]*File{}
	f.unknown = 0
	f.known = 0
	f.path = curPath

	for i := range f.Folders {
		folder := f.Folders[i]
		folder.rebuild(filepath.Join(curPath, folder.Name))
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

func (f *Folder) explorer() {
	app := tview.NewApplication()

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Check if the key pressed is the Escape key
		if event.Key() == tcell.KeyEscape {
			// Stop the application
			app.Stop()
			return nil // Stop event propagation
		}
		if event.Key() == tcell.KeyRune {
			ch := event.Rune()
			if ch == 'q' {
				app.Stop()
				return nil
			}
		}
		return event // Continue processing other events
	})

	list := tview.NewList().ShowSecondaryText(false)

	sort.Slice(f.Folders, func(i, j int) bool {
		a := f.Folders[i]
		b := f.Folders[j]
		if a.size != b.size {
			return a.size < b.size
		}
		return f.Folders[i].Name < f.Folders[j].Name
	})

	offset := 1
	for i := range f.Folders {
		folder := f.Folders[i]
		progress := float64(folder.size) / float64(f.size)
		list.AddItem(fmt.Sprintf("%+8s %s %s", formatSize(folder.size), progressbar(progress, 10), folder.Name+"/"),
			"", ' ', nil)
		// list.SetCellSimple(i+offset, 0, formatSize(folder.size))
		// list.SetCellSimple(i+offset, 1, progressbar(progress, 10))
		// list.SetCell(i+offset, 2, tview.NewTableCell(folder.Name).SetTextColor(tcell.ColorBlue))
	}

	sort.Slice(f.Files, func(i, j int) bool {
		a := f.Files[i]
		b := f.Files[j]
		if a.Size != b.Size {
			return a.Size < b.Size
		}
		return f.Files[i].Name < f.Files[j].Name
	})

	offset += len(f.Folders)
	for i := range f.Files {
		file := f.Files[i]
		progress := float64(file.Size) / float64(f.size)
		list.AddItem(fmt.Sprintf("%+8s %s %s", formatSize(int64(file.Size)), progressbar(progress, 10), file.Name),
			"", ' ', nil)
		// list.SetCellSimple(i+offset, 0, formatSize(int64(file.Size)))
		// list.SetCellSimple(i+offset, 1, progressbar(progress, 10))
		// list.SetCell(i+offset, 2, tview.NewTableCell(file.Name).SetTextColor(tcell.ColorBlue))
	}

	// list.Select(0, 0).SetFixed(1, 1).SetDoneFunc(func(key tcell.Key) {
	// 	if key == tcell.KeyEscape || key == tcell.KeyRune {
	// 		app.Stop()
	// 	}
	// 	if key == tcell.KeyEnter {
	// 		list.SetSelectable(true, true)
	// 	}
	// }).SetSelectedFunc(func(row int, column int) {
	// 	list.GetCell(row, column).SetTextColor(tcell.ColorRed)
	// 	list.SetSelectable(false, false)
	// })

	header := tview.NewTextView().
		SetTextAlign(tview.AlignLeft).
		SetText("--- " + f.path + " (" + formatSize(f.size) + ") ---")

	grid := tview.NewGrid().
		SetRows(1, 0).
		SetColumns(0).
		AddItem(header, 0, 0, 1, 1, 0, 0, false).
		AddItem(list, 1, 0, 1, 1, 0, 0, true)

	// box := tview.NewGrid().SetBorder(true).SetTitle("Explore " + f.path)
	// box.Set

	if err := app.SetRoot(grid, true).SetFocus(list).Run(); err != nil {
		panic(err)
	}
}

var progressRunes = []rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉', '█'}

// progress: 0 - 1.0 (100%)
// width: number of characters
func progressbar(progress float64, width int) string {
	var segPct = 1 / float64(width)
	var full = int(math.Floor(progress / segPct))
	var i = 0

	res := make([]rune, width)
	for ; i < full; i++ {
		res[i] = progressRunes[len(progressRunes)-1]
	}

	rem := progress - float64(full)*segPct
	idx := int(math.Round(rem / segPct * float64(len(progressRunes))))
	res[i] = progressRunes[idx]
	i++

	for ; i < width; i++ {
		res[i] = ' '
	}

	return string(res)
}
