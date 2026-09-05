package importer

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type projectCheckpoint struct {
	Project string       `json:"project_id"`
	Folder  projectState `json:"folder"`
}

func checkpointName(project string) string {
	return fmt.Sprintf("%x.json", sha256.Sum256([]byte(project)))
}

func checkpointProject(path, project string, folder projectState) error {
	return saveJSON(filepath.Join(path+".projects", checkpointName(project)), projectCheckpoint{project, folder})
}

func projectCheckpointFiles(path string) ([]string, error) {
	entries, err := os.ReadDir(path + ".projects")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, entry := range entries {
		// Atomic-write temporary files are never replayed.
		if strings.HasSuffix(entry.Name(), ".json") {
			if !entry.Type().IsRegular() {
				return nil, fmt.Errorf("invalid project checkpoint: %s", entry.Name())
			}
			files = append(files, filepath.Join(path+".projects", entry.Name()))
		}
	}
	return files, nil
}

func replayProjects(path string, s *state) error {
	files, err := projectCheckpointFiles(path)
	if err != nil {
		return err
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var checkpoint projectCheckpoint
		if err = json.Unmarshal(b, &checkpoint); err != nil {
			return fmt.Errorf("invalid project checkpoint %s: %w", file, err)
		}
		if checkpoint.Project == "" || checkpoint.Folder.ID == "" || filepath.Base(file) != checkpointName(checkpoint.Project) {
			return fmt.Errorf("invalid project checkpoint: %s", file)
		}
		s.Projects[checkpoint.Project] = checkpoint.Folder
	}
	return nil
}

func clearProjectCheckpoints(path string) error {
	files, err := projectCheckpointFiles(path)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err = os.Remove(file); err != nil {
			return fmt.Errorf("remove project checkpoint: %w", err)
		}
	}
	return nil
}

type projectState struct {
	ID    string `json:"joplin_id"`
	Title string `json:"title"`
}
type state struct {
	Projects map[string]projectState `json:"projects"`
}

func loadState(path string) (state, error) {
	s := state{}
	b, e := os.ReadFile(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return s, e
	}
	if e == nil {
		if e = json.Unmarshal(b, &s); e != nil {
			return s, fmt.Errorf("invalid state file: %w", e)
		}
	}
	if s.Projects == nil {
		s.Projects = map[string]projectState{}
	}
	return s, replayProjects(path, &s)
}
func saveState(path string, s state) error { return saveJSON(path, s) }

func saveJSON(path string, value any) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if e = enc.Encode(value); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
