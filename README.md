# Outpost

Outpost is a file monitoring and task automation tool that watches your project folders for changes and executes tasks based on your configuration.

## Features

- **Automatic Change Detection**: Monitors project folders using SHA256 checksums
- **Auto-Exec Mode**: Automatically run tasks when changes are detected
- **Interactive Mode**: Hierarchical menu with project → task navigation
- **Run All Options**: Batch execute all tasks from one or all projects
- **Continuous Monitoring**: Background detection even while menus are open
- **Sequential Execution**: Tasks run one at a time, never in parallel
- **Real-time Notifications**: Get notified when changes are detected

## Installation

Build the project:

```bash
go build -o outpost
```

## Configuration

Create a configuration file (e.g., `outpost.yml`):

```yaml
watch:
  root_dir: .
  projects:
    # Example with auto-exec: changes trigger automatic builds
    - name: example1
      path: ./examples/test_project_1
      auto_exec: false
      tasks:
        - name: build
          command: echo "Auto-building..." && sleep 1 && echo "Build complete!"
          workdir: ./examples/test_project_1
        - name: notify
          command: echo "Changes detected and processed!"
          workdir: ./examples/test_project_1

    # Example with manual execution: shows interactive menu
    - name: example2
      path: ./examples/test_project_2
      auto_exec: false
      tasks:
        - name: test
          command: echo "🧪 Running tests..." && sleep 1 && echo "Tests passed!"
          workdir: ./examples/test_project_2
        - name: deploy
          command: echo "🚀 Deploying..." && sleep 1 && echo "✅ Deployed!"
          workdir: ./examples/test_project_2
```

### Configuration Options

- **root_dir**: Base directory for relative paths
- **projects**: Array of projects to monitor
  - **name**: Project identifier
  - **path**: Path to monitor (relative or absolute)
  - **auto_exec**:
    - `true`: Tasks run automatically when changes detected
    - `false`: Shows interactive menu for manual task selection
  - **tasks**: Array of tasks to execute
    - **name**: Task identifier
    - **command**: Shell command to execute
    - **workdir**: Working directory for the command

## Usage

Start monitoring:

```bash
./outpost -config outpost.yml
```

Use `auto_exec: true` to automatically excecute tasks on changes.

If you use manual mode and new changes are detected, refresh the menu by pressing CTRL+C once (pressing CTRL+C quickly twice will shut down Outpost.)
# outpost
