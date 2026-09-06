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

const stateVersion = 1

type projectCheckpoint struct {
	Version     int              `json:"version"`
	Project     string           `json:"project_id"`
	Folder      projectState     `json:"folder"`
	Destination destinationState `json:"destination"`
}

func checkpointName(project string) string {
	return fmt.Sprintf("%x.json", sha256.Sum256([]byte(project)))
}

func checkpointProject(path, project string, folder projectState, destination destinationState) error {
	return saveJSON(filepath.Join(path+".projects", checkpointName(project)), projectCheckpoint{stateVersion, project, folder, destination})
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
		if checkpoint.Version != 0 && checkpoint.Version != stateVersion {
			return fmt.Errorf("unsupported project checkpoint version %d: %s", checkpoint.Version, file)
		}
		if checkpoint.Project == "" || checkpoint.Folder.ID == "" || !checkpoint.Destination.valid() || filepath.Base(file) != checkpointName(checkpoint.Project) {
			return fmt.Errorf("invalid project checkpoint: %s", file)
		}
		if checkpoint.Folder.Title == "" {
			checkpoint.Folder.Title = "ChatGPT project " + checkpoint.Project
		}
		if s.Destination == nil {
			destination := checkpoint.Destination
			s.Destination = &destination
		} else if *s.Destination != checkpoint.Destination {
			return fmt.Errorf("project checkpoint destination does not match state: %s", file)
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
type destinationState struct {
	Endpoint string `json:"endpoint"`
	RootID   string `json:"root_id"`
}

func (d destinationState) valid() bool { return d.Endpoint != "" && d.RootID != "" }

type state struct {
	Version     int                     `json:"version"`
	Destination *destinationState       `json:"destination,omitempty"`
	Projects    map[string]projectState `json:"projects"`
}

func migrateAndValidateState(s *state) error {
	if s.Version != 0 && s.Version != stateVersion {
		return fmt.Errorf("unsupported state version %d; use a compatible importer or migrate the state", s.Version)
	}
	if s.Destination != nil && !s.Destination.valid() {
		return fmt.Errorf("invalid state destination binding")
	}
	if s.Projects == nil {
		s.Projects = map[string]projectState{}
	}
	for project, folder := range s.Projects {
		if project == "" || folder.ID == "" {
			return fmt.Errorf("invalid project mapping for %q", project)
		}
		if folder.Title == "" {
			folder.Title = "ChatGPT project " + project
			s.Projects[project] = folder
		}
	}
	s.Version = stateVersion
	return nil
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
	if e = migrateAndValidateState(&s); e != nil {
		return s, fmt.Errorf("invalid state file: %w", e)
	}
	return s, replayProjects(path, &s)
}
func saveState(path string, s state) error {
	if e := migrateAndValidateState(&s); e != nil {
		return e
	}
	return saveJSON(path, s)
}

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
