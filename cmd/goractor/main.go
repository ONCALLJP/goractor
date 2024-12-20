package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ONCALLJP/goractor/internal/config"
	"github.com/ONCALLJP/goractor/internal/destination"
	"github.com/ONCALLJP/goractor/internal/executor"
	"github.com/ONCALLJP/goractor/internal/prompt"
	"github.com/ONCALLJP/goractor/internal/systemd"
	"github.com/ONCALLJP/goractor/internal/task"
	"github.com/manifoldco/promptui"
	"gopkg.in/yaml.v3"
)

var (
	taskManager        *task.Manager
	configManager      *config.Manager
	configPrompt       *config.Prompt
	destinationManager *destination.Manager
	excutorManager     *executor.Executor
)

func init() {
	var configDir string

	// Fallback to current user's home if not running as sudo or if getting real user failed
	if configDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error getting home directory: %v\n", err)
			os.Exit(1)
		}
		configDir = filepath.Join(homeDir, ".goractor")
	}

	// Initialize config manager
	configPath := filepath.Join(configDir, "config.yaml")
	configManager = config.NewManager(configPath)
	if err := configManager.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	configPrompt = config.NewPrompt()

	// Initialize task manager
	taskPath := filepath.Join(configDir, "tasks.yaml")
	taskManager = task.NewManager(taskPath)
	if err := taskManager.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "error loading tasks: %v\n", err)
		os.Exit(1)
	}

	destPath := filepath.Join(configDir, "destinations.yaml")
	destinationManager = destination.NewManager(destPath)
	if err := destinationManager.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "error loading destinations: %v\n", err)
		os.Exit(1)
	}

	// Initialize executor and systemd
	excutorManager = executor.NewExecutor(configManager.GetDatabases(), destinationManager)
}

func main() {
	if len(os.Args) < 2 {
		showHelp()
		os.Exit(0)
	}

	if err := rootCommand(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func rootCommand() error {
	if len(os.Args) == 2 && os.Args[1] == "--help" {
		showHelp()
		return nil
	}

	switch os.Args[1] {
	case "task":
		return handleTaskCommand(os.Args[2:])
	case "config":
		return handleConfigCommand(os.Args[2:])
	case "destination":
		return handleDestinationCommand(os.Args[2:])
	case "systemd":
		return handleSystemdCommand(os.Args[2:])
	case "log":
		return handleLogCommand(os.Args[2:])
	case "debug":
		return handleDebugCommand(os.Args[2:])
	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func handleTaskCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: goractor task [list|show|add|remove|edit|run] [task-name]")
	}

	switch args[0] {
	case "list":
		return listTasks()
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor task show [task-name]")
		}
		return showTask(args[1])
	case "add":
		return addTask()
	case "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor task remove [task-name]")
		}
		return removeTask(args[1])
	case "edit":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor task edit [task-name]")
		}
		return editTask(args[1])
	case "run":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor task run [task-name]")
		}
		return runTask(args[1])
	default:
		return fmt.Errorf("unknown task command: %s", args[0])
	}
}

func handleConfigCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: goractor config [list|show|add|remove|edit] [database-name]")
	}

	switch args[0] {
	case "list":
		dbs := configManager.ListDatabases()
		if len(dbs) == 0 {
			fmt.Println("No databases configured")
			return nil
		}
		fmt.Println("Configured Databases:")
		for name := range configManager.GetDatabases() {
			fmt.Printf("- %s\n", name)
		}
		return nil

	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor config show [database-name]")
		}
		db, exists := configManager.GetDatabase(args[1])
		if !exists {
			return fmt.Errorf("database %s not found", args[1])
		}
		dbCopy := *db
		dbCopy.Password = "********"
		data, _ := yaml.Marshal(dbCopy)
		fmt.Printf("Database: %s\n%s", args[1], string(data))
		return nil

	case "add":
		fmt.Println("\n⚠️  IMPORTANT SECURITY NOTICE:")
		fmt.Println("- Configuration will contain sensitive information")
		fmt.Println("- You are responsible for securing and backing up configurations")
		if !confirmPrompt("Do you understand and accept these responsibilities?") {
			return fmt.Errorf("configuration creation cancelled")
		}
		name, db, err := configPrompt.PromptDatabase(nil)
		if err != nil {
			return err
		}
		return configManager.AddDatabase(name, db)

	case "edit":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor config edit [database-name]")
		}
		db, exists := configManager.GetDatabase(args[1])
		if !exists {
			return fmt.Errorf("database %s not found", args[1])
		}
		name, updatedDB, err := configPrompt.PromptDatabase(db)
		if err != nil {
			return err
		}
		return configManager.UpdateDatabase(name, updatedDB)

	case "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor config remove [database-name]")
		}
		return configManager.RemoveDatabase(args[1])

	default:
		return fmt.Errorf("unknown config command: %s", args[0])
	}
}

func handleDestinationCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: goractor destination [list|show|add|remove|edit] [destination-name]")
	}

	switch args[0] {
	case "list":
		dests := destinationManager.List()
		if len(dests) == 0 {
			fmt.Println("No destinations configured")
			return nil
		}
		fmt.Println("Configured Destinations:")
		for _, name := range dests {
			fmt.Printf("- %s\n", name)
		}
		return nil

	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor destination show [destination-name]")
		}
		dest, exists := destinationManager.Get(args[1])
		if !exists {
			return fmt.Errorf("destination %s not found", args[1])
		}
		// Hide sensitive values
		dest.Token.Value = "********"
		data, _ := yaml.Marshal(dest)
		fmt.Printf("Destination: %s\n%s", args[1], string(data))
		return nil

	case "add":
		fmt.Println("\n⚠️  IMPORTANT SECURITY NOTICE:")
		fmt.Println("- Configuration will contain sensitive information")
		fmt.Println("- You are responsible for securing and backing up configurations")
		if !confirmPrompt("Do you understand and accept these responsibilities?") {
			return fmt.Errorf("configuration creation cancelled")
		}
		return addDestination()

	case "edit":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor destination edit [destination-name]")
		}
		return editDestination(args[1])

	case "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor destination remove [destination-name]")
		}
		return destinationManager.Remove(args[1])

	default:
		return fmt.Errorf("unknown destination command: %s", args[0])
	}
}

func handleSystemdCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: goractor systemd [install|enable|disable|status] [task-name]")
	}

	switch args[0] {
	case "install":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor systemd install [task-name]")
		}
		return installTask(args[1])

	case "enable":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor systemd enable [task-name]")
		}
		return enableTask(args[1])

	case "disable":
		if len(args) != 2 {
			return fmt.Errorf("usage: goractor systemd disable [task-name]")
		}
		return disableTask(args[1])

	case "status":
		if len(args) == 1 {
			return showAllTaskStatus()
		}
		return showTaskStatus()

	case "restart":
		taskName := args[1]
		restartScript := fmt.Sprintf(`
				systemctl restart goractor-%[1]s.service
				systemctl restart goractor-%[1]s.timer
				exit
		`, taskName)

		cmd := exec.Command("sudo", "bash", "-c", restartScript)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to restart service: %w", err)
		}

		fmt.Printf("Successfully restarted service and timer for task %s\n", taskName)
		return nil

	default:
		return fmt.Errorf("unknown systemd command: %s", args[0])
	}
}

func handleLogCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: goractor log [clean|show]")
	}

	switch args[0] {
	case "clean":
		commands := [][]string{
			// Truncate log files
			{"truncate", "-s", "0", "/var/log/goractor.log"},
			{"truncate", "-s", "0", "/var/log/goractor.error.log"},
			// Reset permissions just in case
			{"chown", fmt.Sprintf("%s:%s", os.Getenv("USER"), os.Getenv("USER")), "/var/log/goractor.log", "/var/log/goractor.error.log"},
			{"chmod", "644", "/var/log/goractor.log", "/var/log/goractor.error.log"},
		}

		for _, cmd := range commands {
			command := exec.Command("sudo", cmd...)
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			if err := command.Run(); err != nil {
				return fmt.Errorf("failed to execute command %v: %w", cmd, err)
			}
		}

		fmt.Println("Log files cleaned successfully")
		return nil

	case "show":
		// Show last N lines of logs
		cmd := exec.Command("tail", "-n", "50", "/var/log/goractor.log", "/var/log/goractor.error.log")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()

	default:
		return fmt.Errorf("unknown log command: %s", args[0])
	}
}

func handleDebugCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: goractor debug [task-name]")
	}
	taskName := args[0]

	fmt.Printf("=== Debug Info for Task: %s ===\n\n", taskName)

	// 1. Service File Content
	fmt.Println("1. Service File (/etc/systemd/system/goractor-" + taskName + ".service):")
	fmt.Println("----------------------------------------")
	serviceCmd := exec.Command("cat", fmt.Sprintf("/etc/systemd/system/goractor-%s.service", taskName))
	serviceContent, err := serviceCmd.Output()
	if err != nil {
		fmt.Printf("Error reading service file: %v\n", err)
	} else {
		fmt.Printf("%s\n", serviceContent)
	}

	// 2. Timer File Content
	fmt.Println("\n2. Timer File (/etc/systemd/system/goractor-" + taskName + ".timer):")
	fmt.Println("----------------------------------------")
	timerCmd := exec.Command("cat", fmt.Sprintf("/etc/systemd/system/goractor-%s.timer", taskName))
	timerContent, err := timerCmd.Output()
	if err != nil {
		fmt.Printf("Error reading timer file: %v\n", err)
	} else {
		fmt.Printf("%s\n", timerContent)
	}

	// 3. Service Status
	fmt.Println("\n3. Service Status:")
	fmt.Println("----------------------------------------")
	serviceStatusCmd := exec.Command("systemctl", "status", fmt.Sprintf("goractor-%s.service", taskName))
	serviceStatus, _ := serviceStatusCmd.Output()
	fmt.Printf("%s\n", serviceStatus)

	// 4. Timer Status
	fmt.Println("\n4. Timer Status:")
	fmt.Println("----------------------------------------")
	timerStatusCmd := exec.Command("systemctl", "status", fmt.Sprintf("goractor-%s.timer", taskName))
	timerStatus, _ := timerStatusCmd.Output()
	fmt.Printf("%s\n", timerStatus)

	// 5. Check if binary exists
	fmt.Println("\n5. Binary Check:")
	fmt.Println("----------------------------------------")
	binaryPath := "/home/ubuntu/goractor/goractor"
	if _, err := os.Stat(binaryPath); err != nil {
		fmt.Printf("Binary not found at %s\n", binaryPath)
	} else {
		binInfo, err := os.Stat(binaryPath)
		if err == nil {
			fmt.Printf("Binary exists: %s (Size: %d bytes, Mode: %s)\n",
				binaryPath, binInfo.Size(), binInfo.Mode())
		}
	}

	// 6. Log Files
	fmt.Println("\n6. Recent Logs:")
	fmt.Println("----------------------------------------")
	fmt.Println("Last 5 lines of error log:")
	errorLogCmd := exec.Command("tail", "-n", "5", "/var/log/goractor.error.log")
	errorLog, _ := errorLogCmd.Output()
	fmt.Printf("%s\n", errorLog)

	return nil
}

func showTaskStatus() error {
	cmd := exec.Command("systemctl", "list-timers", "goractor-*")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get timer status: %w", err)
	}

	fmt.Println("Goractor Tasks Status:")
	fmt.Println("=====================")

	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, "goractor-") {
			taskName := extractTaskName(line)
			if strings.Contains(line, " - ") {
				fmt.Printf("%s: INACTIVE\n", taskName)
			} else {
				nextRun := extractNextRun(line)
				lastRun := extractLastRun(line)
				fmt.Printf("%s: ACTIVE\n", taskName)
				fmt.Printf("  Next Run: %s\n", nextRun)
				fmt.Printf("  Last Run: %s\n", lastRun)
			}
		}
	}

	return nil
}

func extractTaskName(line string) string {
	if idx := strings.Index(line, "goractor-"); idx != -1 {
		name := line[idx:]
		if spaceIdx := strings.Index(name, " "); spaceIdx != -1 {
			name = name[:spaceIdx]
		}
		return strings.TrimPrefix(name, "goractor-")
	}
	return ""
}

func extractNextRun(line string) string {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		return fields[0] + " " + fields[1]
	}
	return "Unknown"
}

func extractLastRun(line string) string {
	fields := strings.Fields(line)
	for i, field := range fields {
		if field == "ago" && i >= 3 {
			return fields[i-3] + " " + fields[i-2]
		}
	}
	return "Unknown"
}

func addDestination() error {
	prompt := destination.NewPrompt()
	name, dest, err := prompt.PromptDestination(nil)
	if err != nil {
		return err
	}
	return destinationManager.Add(name, dest)
}

func editDestination(currentName string) error {
	dest, exists := destinationManager.Get(currentName)
	if !exists {
		return fmt.Errorf("destination %s not found", currentName)
	}

	prompt := destination.NewPrompt()
	newName, updatedDest, err := prompt.PromptDestination(&dest)
	if err != nil {
		return err
	}

	// If name has changed
	if newName != currentName {
		// Remove old destination
		if err := destinationManager.Remove(currentName); err != nil {
			return fmt.Errorf("failed to remove old destination: %w", err)
		}
		// Add with new name
		if err := destinationManager.Add(newName, updatedDest); err != nil {
			// Try to restore old destination if adding new one fails
			_ = destinationManager.Add(currentName, dest)
			return fmt.Errorf("failed to add destination with new name: %w", err)
		}
		fmt.Printf("Destination renamed from %s to %s\n", currentName, newName)
	} else {
		// Just update if name hasn't changed
		if err := destinationManager.Update(currentName, updatedDest); err != nil {
			return fmt.Errorf("failed to update destination: %w", err)
		}
	}

	fmt.Printf("Successfully updated destination %s\n", newName)
	return nil
}

// Placeholder functions - we'll implement these next
func listTasks() error {
	tasks := taskManager.List()

	if len(tasks) == 0 {
		fmt.Println("No tasks configured")
		return nil
	}

	fmt.Println("Tasks:")
	for _, t := range tasks {
		fmt.Printf("- %s\n", t.Name)
	}
	return nil
}

func showTask(name string) error {
	task, err := taskManager.Get(name)
	if err != nil {
		return err
	}

	fmt.Println(task.String())
	return nil
}

func addTask() error {
	taskPrompt := prompt.NewTaskPrompt(destinationManager, configManager)
	newTask, err := taskPrompt.CreateTask()
	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	if err := taskManager.Add(*newTask); err != nil {
		return fmt.Errorf("failed to save task: %w", err)
	}

	fmt.Printf("Successfully created task: %s\n", newTask.Name)
	return nil
}

func editTask(name string) error {
	currentTask, err := taskManager.Get(name)
	if err != nil {
		return err
	}

	taskPrompt := prompt.NewTaskPrompt(destinationManager, configManager)
	if err := taskPrompt.EditTask(&currentTask); err != nil {
		return fmt.Errorf("failed to edit task: %w", err)
	}

	if err := taskManager.Update(currentTask); err != nil {
		return fmt.Errorf("failed to save task: %w", err)
	}

	fmt.Printf("Successfully updated task: %s\n", name)
	return nil
}

func removeTask(name string) error {
	return taskManager.Remove(name)
}

func runTask(name string) error {
	task, err := taskManager.Get(name)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Printf("Runing task '%s'...\n\n", name)
	if err := excutorManager.Run(ctx, &task); err != nil {
		fmt.Printf("\n❌ Test failed: %v\n", err)
		return err
	}
	return nil
}

func installTask(name string) error {
	task, err := taskManager.Get(name)
	if err != nil {
		return err
	}

	// Get absolute path of binary
	// exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	generator := systemd.NewServiceGenerator()
	if err := generator.GenerateService(&task); err != nil {
		return err
	}

	fmt.Printf("Generated systemd service files for task %s\n", name)

	return nil
}

func enableTask(name string) error {
	// Run systemctl commands using exec
	if err := exec.Command("systemctl", "enable", fmt.Sprintf("goractor-%s.timer", name)).Run(); err != nil {
		return fmt.Errorf("failed to enable task: %w", err)
	}
	if err := exec.Command("systemctl", "start", fmt.Sprintf("goractor-%s.timer", name)).Run(); err != nil {
		return fmt.Errorf("failed to start task: %w", err)
	}
	return nil
}

func disableTask(taskName string) error {

	disableScript := fmt.Sprintf(`
			# Stop and disable timer
			systemctl stop goractor-%[1]s.timer
			systemctl disable goractor-%[1]s.timer

			# Stop service if running
			systemctl stop goractor-%[1]s.service

			# Remove service and timer files
			rm -f /etc/systemd/system/goractor-%[1]s.service
			rm -f /etc/systemd/system/goractor-%[1]s.timer

			# Reload systemd
			systemctl daemon-reload

			exit
	`, taskName)

	cmd := exec.Command("sudo", "bash", "-c", disableScript)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to disable service: %w", err)
	}

	fmt.Printf("Successfully disabled and removed service for task %s\n", taskName)
	return nil
}

func showAllTaskStatus() error {
	cmd := exec.Command("systemctl", "list-timers", "goractor-*")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func confirmPrompt(message string) bool {
	prompt := promptui.Prompt{
		Label:     message,
		IsConfirm: true,
	}
	result, err := prompt.Run()
	if err != nil {
		return false
	}
	return strings.ToLower(result) == "y"
}

func showHelp() {
	help := `
Goractor - PostgreSQL Task Scheduler

What you can do:
• Schedule PostgreSQL queries to run automatically
• Send results to Slack or other API endpoints
• Export query results as CSV
• Manage multiple database connections
• Monitor and debug task execution

Usage:
goractor [command] [subcommand] [arguments]

Available Commands:
config        Configure database connections
	add          Add new database connection
	list         Show configured databases
	remove       Remove a database
	show         Display database details

destination   Set up where to send results
	add          Add new destination (Slack/API)
	list         Show configured destinations
	remove       Remove a destination
	show         Display destination details

task          Manage scheduled tasks
	add          Create new scheduled task
	list         Show all tasks
	remove       Remove a task
	show         Display task details
	run          Test task execution

systemd       Control task scheduling
	install      Set up task schedule
	restart      Restart task execution
	disable      Stop task execution
	status       Check scheduler status

debug         Troubleshoot task issues
log           View or clear execution logs

Common Examples:
1. Set up a new scheduled task:
	 goractor task add

2. Test task before scheduling:
	 goractor task run task1

3. Start scheduling:
	 goractor systemd install task1

4. Check execution status:
	 goractor systemd status

5. View execution logs:
	 goractor log show

Use "goractor [command] --help" for detailed information about each command.
`
	fmt.Println(help)
}
