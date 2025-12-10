package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/manifoldco/promptui"
	"gopkg.in/yaml.v3"
)

type Task struct {
	// name of the task
	Name string `yaml:"name"`
	// command to execute
	Command string `yaml:"command"`
	// Workdir to change to before executing the command
	WorkDir string `yaml:"workdir"`
}

type ProjectConfig struct {
	// name of the project/config
	Name string `yaml:"name"`
	// Path to the project folder (can be relative or absolute)
	Path string `yaml:"path"`
	// Should the tasks be executed automatically
	AutoExec bool `yaml:"auto_exec"`
	// Tasks to execute when changes are detected
	Tasks []Task `yaml:"tasks"`
}

type WatchConfig struct {
	RootDir  string          `yaml:"root_dir"`
	Projects []ProjectConfig `yaml:"projects"`
}

type Config struct {
	Watch WatchConfig `yaml:"watch"`
}

type ProjectState struct {
	Name      string
	Path      string
	Checksum  string
	Changed   bool
	AutoExec  bool
	Tasks     []Task
	LastCheck time.Time
}

type Outpost struct {
	Config         *Config
	States         map[string]*ProjectState
	Mutex          sync.Mutex
	TaskQueue      chan TaskExecution
	IsProcessing   bool
	ProcessingLock sync.Mutex
}

type TaskExecution struct {
	Project string
	Task    Task
}

func NewOutpost(config *Config) *Outpost {
	return &Outpost{
		Config:    config,
		States:    make(map[string]*ProjectState),
		TaskQueue: make(chan TaskExecution, 100),
	}
}

// LoadConfig loads the configuration from a YAML file
func LoadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// CalculateChecksum calculates SHA256 checksum of all files in a directory
func CalculateChecksum(path string) (string, error) {
	hasher := sha256.New()
	var files []string

	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and hidden files
		if info.IsDir() || strings.HasPrefix(filepath.Base(filePath), ".") {
			return nil
		}

		files = append(files, filePath)
		return nil
	})

	if err != nil {
		return "", err
	}

	// Sort files for consistent checksums
	sort.Strings(files)

	for _, filePath := range files {
		file, err := os.Open(filePath)
		if err != nil {
			// Skip files we can't read
			continue
		}

		if _, err := io.Copy(hasher, file); err != nil {
			file.Close()
			continue
		}
		file.Close()

		// Also hash the relative path to detect file renames/moves
		relPath, _ := filepath.Rel(path, filePath)
		hasher.Write([]byte(relPath))
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// InitializeStates initializes the state for all projects
func (o *Outpost) InitializeStates() error {
	for _, project := range o.Config.Watch.Projects {
		absPath, err := filepath.Abs(filepath.Join(o.Config.Watch.RootDir, project.Path))
		if err != nil {
			return fmt.Errorf("failed to resolve path for project %s: %w", project.Name, err)
		}

		checksum, err := CalculateChecksum(absPath)
		if err != nil {
			return fmt.Errorf("failed to calculate checksum for project %s: %w", project.Name, err)
		}

		o.States[project.Name] = &ProjectState{
			Name:      project.Name,
			Path:      absPath,
			Checksum:  checksum,
			Changed:   false,
			AutoExec:  project.AutoExec,
			Tasks:     project.Tasks,
			LastCheck: time.Now(),
		}
	}

	return nil
}

// CheckForChanges checks all projects for changes
func (o *Outpost) CheckForChanges() {
	o.Mutex.Lock()
	defer o.Mutex.Unlock()

	for name, state := range o.States {
		newChecksum, err := CalculateChecksum(state.Path)
		if err != nil {
			log.Printf("Error calculating checksum for %s: %v", name, err)
			continue
		}

		if newChecksum != state.Checksum {
			if !state.Changed {
				color.Yellow("📦 Change detected in project: %s", name)
				state.Changed = true
			}
			state.Checksum = newChecksum
		}
	}
}

// GetChangedProjects returns a list of projects that have changed
func (o *Outpost) GetChangedProjects() []*ProjectState {
	o.Mutex.Lock()
	defer o.Mutex.Unlock()

	var changed []*ProjectState
	for _, state := range o.States {
		if state.Changed {
			changed = append(changed, state)
		}
	}

	return changed
}

// ExecuteTask executes a single task
func (o *Outpost) ExecuteTask(project string, task Task) error {
	color.Cyan("\n🚀 Executing task '%s' for project '%s'", task.Name, project)

	workDir := task.WorkDir
	if workDir == "" {
		workDir = o.States[project].Path
	} else {
		// Make workdir absolute if relative
		if !filepath.IsAbs(workDir) {
			workDir = filepath.Join(o.Config.Watch.RootDir, workDir)
		}
	}

	// Parse command for shell execution
	cmd := exec.Command("sh", "-c", task.Command)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

	if err != nil {
		color.Red("❌ Task '%s' failed after %v: %v", task.Name, duration, err)
		return err
	}

	color.Green("✅ Task '%s' completed successfully in %v", task.Name, duration)
	return nil
}

// ProcessTaskQueue processes tasks from the queue one at a time
func (o *Outpost) ProcessTaskQueue() {
	for taskExec := range o.TaskQueue {
		o.ProcessingLock.Lock()
		o.IsProcessing = true
		o.ProcessingLock.Unlock()

		err := o.ExecuteTask(taskExec.Project, taskExec.Task)
		if err != nil {
			log.Printf("Task execution failed: %v", err)
		}

		// Mark project as no longer changed after processing
		o.Mutex.Lock()
		if state, ok := o.States[taskExec.Project]; ok {
			state.Changed = false
		}
		o.Mutex.Unlock()

		o.ProcessingLock.Lock()
		o.IsProcessing = false
		o.ProcessingLock.Unlock()
	}
}

// HandleAutoExecProjects handles projects with AutoExec enabled
func (o *Outpost) HandleAutoExecProjects() {
	changed := o.GetChangedProjects()

	for _, state := range changed {
		if state.AutoExec {
			// Queue all tasks for this project
			for _, task := range state.Tasks {
				o.TaskQueue <- TaskExecution{
					Project: state.Name,
					Task:    task,
				}
			}
		}
	}
}

// ShowProjectMenu shows a menu to select a project with real-time updates
func (o *Outpost) ShowProjectMenu(manualProjects []*ProjectState) (*ProjectState, bool, error) {
	if len(manualProjects) == 0 {
		return nil, false, nil
	}

	type ProjectMenuItem struct {
		Label      string
		Project    *ProjectState
		RunAllFlag bool
	}

	// Build menu items
	var menuItems []ProjectMenuItem

	// Add "Run all" option at the top
	totalTasks := 0
	for _, state := range manualProjects {
		totalTasks += len(state.Tasks)
	}
	menuItems = append(menuItems, ProjectMenuItem{
		Label:      fmt.Sprintf("⚡ Run all tasks from all projects (%d tasks total)", totalTasks),
		RunAllFlag: true,
	})

	// Add individual projects
	for _, state := range manualProjects {
		menuItems = append(menuItems, ProjectMenuItem{
			Label:   fmt.Sprintf("📦 %s (%d tasks)", state.Name, len(state.Tasks)),
			Project: state,
		})
	}

	templates := &promptui.SelectTemplates{
		Label:    "{{ . }}",
		Active:   "▸ {{ .Label | cyan }}",
		Inactive: "  {{ .Label }}",
		Selected: "✓ {{ .Label | green }}",
	}

	prompt := promptui.Select{
		Label:     "Select a project (↑/↓ to navigate, Enter to select, Ctrl+C to finish)",
		Items:     menuItems,
		Templates: templates,
		Size:      15,
	}

	idx, _, err := prompt.Run()
	if err != nil {
		return nil, false, err
	}

	selected := menuItems[idx]
	if selected.RunAllFlag {
		return nil, true, nil // true = run all
	}

	return selected.Project, false, nil
}

// ShowTaskMenu shows a menu to select a task from a project
func (o *Outpost) ShowTaskMenu(project *ProjectState) (*Task, bool, bool, error) {
	type TaskMenuItem struct {
		Label      string
		Task       *Task
		BackOption bool
		RunAllFlag bool
	}

	var menuItems []TaskMenuItem

	// Add "Run all tasks" option at the top
	menuItems = append(menuItems, TaskMenuItem{
		Label:      fmt.Sprintf("⚡ Run all tasks from this project (%d tasks)", len(project.Tasks)),
		RunAllFlag: true,
	})

	// Add individual tasks
	for i := range project.Tasks {
		menuItems = append(menuItems, TaskMenuItem{
			Label: fmt.Sprintf("🔨 %s", project.Tasks[i].Name),
			Task:  &project.Tasks[i],
		})
	}

	// Add back option
	menuItems = append(menuItems, TaskMenuItem{
		Label:      "← Back to project selection",
		BackOption: true,
	})

	templates := &promptui.SelectTemplates{
		Label:    "{{ . }}",
		Active:   "▸ {{ .Label | cyan }}",
		Inactive: "  {{ .Label }}",
		Selected: "✓ {{ .Label | green }}",
	}

	prompt := promptui.Select{
		Label:     fmt.Sprintf("Select a task for '%s' (↑/↓ to navigate, Enter to select, Ctrl+C to finish)", project.Name),
		Items:     menuItems,
		Templates: templates,
		Size:      15,
	}

	idx, _, err := prompt.Run()
	if err != nil {
		return nil, false, false, err
	}

	selected := menuItems[idx]
	if selected.BackOption {
		return nil, true, false, nil // true = go back, false = not run all
	}
	if selected.RunAllFlag {
		return nil, false, true, nil // false = not back, true = run all
	}

	return selected.Task, false, false, nil
}

// ShowInteractiveMenu shows an interactive hierarchical menu for manual task selection
func (o *Outpost) ShowInteractiveMenu() error {
	for {
		// Get current changed projects (re-check each time)
		o.Mutex.Lock()
		var manualProjects []*ProjectState
		for _, state := range o.States {
			if state.Changed && !state.AutoExec {
				manualProjects = append(manualProjects, state)
			}
		}
		o.Mutex.Unlock()

		if len(manualProjects) == 0 {
			return nil // No more changed manual projects
		}

		// Show changed projects summary
		color.Yellow("\n📋 Changed projects:")
		for _, proj := range manualProjects {
			color.Yellow("   • %s (%d tasks)", proj.Name, len(proj.Tasks))
		}
		fmt.Println()

		// Select a project or run all
		selectedProject, runAll, err := o.ShowProjectMenu(manualProjects)
		if err != nil {
			// User pressed Ctrl+C, exit menu system
			return err
		}

		// Handle "Run all" option
		if runAll {
			color.Cyan("\n⚡ Running all tasks from all projects...\n")
			for _, project := range manualProjects {
				for _, task := range project.Tasks {
					err := o.ExecuteTask(project.Name, task)
					if err != nil {
						log.Printf("Task execution failed: %v", err)
					}
				}
				// Mark project as done after all tasks
				o.Mutex.Lock()
				project.Changed = false
				o.Mutex.Unlock()
				color.Green("✅ All tasks completed for project '%s'\n", project.Name)
			}
			color.Green("✅ All projects completed!\n")
			continue // Go back to monitoring (or next round if new changes detected)
		}

		if selectedProject == nil {
			return nil
		}

		// Work with selected project until user goes back
		for {
			// Check if project still has changes (might have been updated)
			o.Mutex.Lock()
			stillChanged := selectedProject.Changed
			o.Mutex.Unlock()

			if !stillChanged {
				color.Green("✅ Project '%s' has been handled", selectedProject.Name)
				break // Go back to project selection
			}

			// Select a task from the project
			selectedTask, goBack, runAllTasks, err := o.ShowTaskMenu(selectedProject)
			if err != nil {
				// User pressed Ctrl+C, exit entire menu system
				return err
			}
			if goBack {
				// User selected back option
				break
			}

			// Handle "Run all tasks" option
			if runAllTasks {
				color.Cyan("\n⚡ Running all tasks from '%s'...\n", selectedProject.Name)
				for _, task := range selectedProject.Tasks {
					err := o.ExecuteTask(selectedProject.Name, task)
					if err != nil {
						log.Printf("Task execution failed: %v", err)
					}
				}
				// Mark project as done after all tasks
				o.Mutex.Lock()
				selectedProject.Changed = false
				o.Mutex.Unlock()
				color.Green("✅ All tasks completed for '%s'\n", selectedProject.Name)
				break // Go back to project selection
			}

			if selectedTask == nil {
				break
			}

			// Execute the selected task
			err = o.ExecuteTask(selectedProject.Name, *selectedTask)
			if err != nil {
				log.Printf("Task execution failed: %v", err)
				// Continue to allow retry or selecting another task
			}

			// Ask if user wants to mark project as done or continue with another task
			color.Cyan("\n")
			type DoneMenuItem struct {
				Label      string
				MarkAsDone bool
			}

			doneOptions := []DoneMenuItem{
				{Label: "▶ Run another task from this project", MarkAsDone: false},
				{Label: "✓ Mark this project as done", MarkAsDone: true},
			}

			templates := &promptui.SelectTemplates{
				Label:    "{{ . }}",
				Active:   "▸ {{ .Label | cyan }}",
				Inactive: "  {{ .Label }}",
				Selected: "✓ {{ .Label | green }}",
			}

			donePrompt := promptui.Select{
				Label:     "What next?",
				Items:     doneOptions,
				Templates: templates,
				Size:      5,
			}

			doneIdx, _, err := donePrompt.Run()
			if err != nil {
				// User pressed Ctrl+C
				return err
			}

			if doneOptions[doneIdx].MarkAsDone {
				// Mark project as done
				o.Mutex.Lock()
				selectedProject.Changed = false
				o.Mutex.Unlock()
				color.Green("✅ Project '%s' marked as done\n", selectedProject.Name)
				break // Go back to project selection
			}
			// Otherwise, loop to show task menu again
		}
	}
}

// Run starts the main monitoring loop
func (o *Outpost) Run() error {
	color.Green("🎯 Outpost is now monitoring %d projects...\n", len(o.Config.Watch.Projects))

	// Start task queue processor
	go o.ProcessTaskQueue()

	// Start continuous background monitoring
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			<-ticker.C
			o.CheckForChanges()
			o.HandleAutoExecProjects()
		}
	}()

	// Main menu loop
	for {
		// Check if there are manual projects with changes
		o.ProcessingLock.Lock()
		isProcessing := o.IsProcessing
		o.ProcessingLock.Unlock()

		if !isProcessing {
			changed := o.GetChangedProjects()
			hasManualProjects := false
			for _, state := range changed {
				if !state.AutoExec {
					hasManualProjects = true
					break
				}
			}

			if hasManualProjects {
				err := o.ShowInteractiveMenu()
				if err != nil && err.Error() != "^C" {
					log.Printf("Menu error: %v", err)
				}
			}
		}

		// Small sleep to avoid busy loop
		time.Sleep(500 * time.Millisecond)
	}
}

func main() {
	configPath := flag.String("config", "outpost.yml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	config, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create Outpost instance
	outpost := NewOutpost(config)

	// Initialize project states
	if err := outpost.InitializeStates(); err != nil {
		log.Fatalf("Failed to initialize project states: %v", err)
	}

	color.Cyan("🏰 Outpost started!")
	color.White("Monitoring projects:")
	for _, project := range config.Watch.Projects {
		autoExecStr := "manual"
		if project.AutoExec {
			autoExecStr = "auto"
		}
		color.White("  • %s (%s) [%s]", project.Name, project.Path, autoExecStr)
	}
	fmt.Println()

	// Run the monitoring loop
	if err := outpost.Run(); err != nil {
		log.Fatalf("Error running Outpost: %v", err)
	}
}
