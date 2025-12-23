package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	box := tview.NewBox().SetBorder(true).SetTitle("Explore " + f.path)
	if err := tview.NewApplication().SetRoot(box, true).Run(); err != nil {
		panic(err)
	}
}
