package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
)

type Project struct {
	Name      string
	Dir       string
	VideoPath string
	Thumbnail string
	Duration  float64
	Width     int
	Height    int
	HasExport bool
}

func Projects(root string) ([]Project, error) {
	names, err := Sessions(root)
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	projects := make([]Project, 0, len(names))
	for _, name := range names {
		p, err := ProjectInfo(root, name)
		if err == nil {
			projects = append(projects, p)
		}
	}
	return projects, nil
}

func ProjectInfo(root, name string) (Project, error) {
	dir := filepath.Join(root, name)
	recording := filepath.Join(dir, "recording.mp4")
	if _, err := os.Stat(recording); err != nil {
		return Project{}, err
	}
	p := Project{Name: name, Dir: dir, VideoPath: recording, Thumbnail: filepath.Join(dir, "thumbnail.jpg")}
	if output := filepath.Join(dir, "output.mp4"); fileExists(output) {
		p.VideoPath, p.HasExport = output, true
	}
	var probe struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	b, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height:format=duration", "-of", "json", p.VideoPath).Output()
	if err != nil {
		return Project{}, fmt.Errorf("probe %s: %w", name, err)
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return Project{}, err
	}
	if len(probe.Streams) > 0 {
		p.Width, p.Height = probe.Streams[0].Width, probe.Streams[0].Height
	}
	p.Duration, _ = strconv.ParseFloat(probe.Format.Duration, 64)
	return p, nil
}

func EnsureThumbnail(p Project) error {
	if fileExists(p.Thumbnail) {
		return nil
	}
	seek := p.Duration * 0.2
	if seek > 2 {
		seek = 2
	}
	return exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-ss", strconv.FormatFloat(seek, 'f', 2, 64), "-i", p.VideoPath,
		"-frames:v", "1", "-vf", "scale=560:350:force_original_aspect_ratio=decrease,pad=560:350:(ow-iw)/2:(oh-ih)/2:color=0x111114",
		"-q:v", "3", p.Thumbnail).Run()
}

func fileExists(path string) bool {
	i, err := os.Stat(path)
	return err == nil && i.Mode().IsRegular() && i.Size() > 0
}
