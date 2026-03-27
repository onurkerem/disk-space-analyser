package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"disk-space-analyser/internal/daemon"
	"disk-space-analyser/internal/db"
	"disk-space-analyser/internal/scanner"
	"disk-space-analyser/internal/server"

	"net/http"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		rootPath := "/"
		if len(os.Args) >= 3 {
			rootPath = os.Args[2]
		}
		cmdStart(rootPath)

	case "stop":
		cmdStop()

	case "status":
		cmdStatus()

	case "_daemon":
		// Hidden subcommand — only invoked by the forked child process.
		if len(os.Args) < 3 {
			log.Fatal("_daemon requires a root path argument")
		}
		runDaemon(os.Args[2])

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [arguments]\n\n", filepath.Base(os.Args[0]))
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  start [path]   Start the daemon (default path: /)")
	fmt.Fprintln(os.Stderr, "  stop           Stop the running daemon")
	fmt.Fprintln(os.Stderr, "  status         Show daemon status")
}

// cmdStart forks the daemon process in the background.
func cmdStart(rootPath string) {
	pidFilePath, err := daemon.PIDPath()
	if err != nil {
		log.Fatalf("resolve pid path: %v", err)
	}

	// Check if already running.
	if pid, err := daemon.ReadPID(pidFilePath); err == nil {
		if daemon.IsRunning(pid) {
			fmt.Fprintf(os.Stderr, "Daemon is already running (PID: %d)\n", pid)
			os.Exit(1)
		}
		// Stale PID file — clean it up.
		_ = daemon.RemovePID(pidFilePath)
	}

	// Ensure data directory exists.
	if _, err := daemon.DataDir(); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	// Open log file for daemon output.
	logFilePath, err := daemon.LogPath()
	if err != nil {
		log.Fatalf("resolve log path: %v", err)
	}
	logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatalf("open log file %s: %v", logFilePath, err)
	}
	defer logFile.Close()

	// Fork self as daemon.
	cmd := exec.Command(os.Args[0], "_daemon", rootPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		log.Fatalf("fork daemon: %v", err)
	}
	// We don't Wait — the child detaches via Setsid.

	// Wait briefly for the child to write its PID file.
	childPID := cmd.Process.Pid
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(pidFilePath); err == nil {
			// Read the actual PID the child wrote (it may differ from cmd.Process.Pid
			// on some systems due to Setsid).
			if actualPID, err := daemon.ReadPID(pidFilePath); err == nil {
				childPID = actualPID
			}
			fmt.Printf("Daemon started (PID: %d)\n", childPID)
			return
		}
	}

	// PID file didn't appear — child may have crashed. Check log.
	fmt.Fprintf(os.Stderr, "Daemon may have failed to start (PID file not created). Check %s\n", logFilePath)
	os.Exit(1)
}

// cmdStop sends SIGTERM to the daemon via the PID file.
func cmdStop() {
	pidFilePath, err := daemon.PIDPath()
	if err != nil {
		log.Fatalf("resolve pid path: %v", err)
	}

	pid, err := daemon.ReadPID(pidFilePath)
	if err != nil {
		fmt.Println("Daemon is not running (no PID file)")
		os.Exit(0)
	}

	if !daemon.IsRunning(pid) {
		fmt.Println("Daemon is not running (stale PID file)")
		_ = daemon.RemovePID(pidFilePath)
		os.Exit(0)
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		log.Fatalf("send SIGTERM to %d: %v", pid, err)
	}

	// Wait briefly for the process to exit.
	for i := 0; i < 20; i++ {
		time.Sleep(250 * time.Millisecond)
		if !daemon.IsRunning(pid) {
			fmt.Printf("Daemon stopped (PID: %d)\n", pid)
			return
		}
	}

	fmt.Fprintf(os.Stderr, "Daemon did not stop gracefully, sending SIGKILL\n")
	_ = syscall.Kill(pid, syscall.SIGKILL)
	// Wait for SIGKILL to take effect.
	for i := 0; i < 10; i++ {
		time.Sleep(200 * time.Millisecond)
		if !daemon.IsRunning(pid) {
			break
		}
	}
	_ = daemon.RemovePID(pidFilePath)
	fmt.Printf("Daemon killed (PID: %d)\n", pid)
}

// cmdStatus prints the daemon status from the PID file and status.json.
func cmdStatus() {
	pidFilePath, err := daemon.PIDPath()
	if err != nil {
		log.Fatalf("resolve pid path: %v", err)
	}

	pid, err := daemon.ReadPID(pidFilePath)
	pidExists := err == nil
	running := pidExists && daemon.IsRunning(pid)

	// Read status file regardless of daemon state.
	statusFilePath, err := daemon.StatusPath()
	if err != nil {
		log.Fatalf("resolve status path: %v", err)
	}
	status, _ := daemon.ReadStatus(statusFilePath) // zero-value OK if missing

	if running {
		fmt.Printf("Daemon running (PID: %d)\n", pid)
	} else if pidExists {
		fmt.Println("Daemon is not running (stale PID file)")
	} else if status.ScannedAt != "" {
		fmt.Println("Daemon is not running")
	} else {
		fmt.Println("Daemon has not been started yet")
	}

	// Show last scan metadata when available.
	if status.ScannedAt != "" {
		fmt.Printf("  Root path:          %s\n", status.RootPath)
		fmt.Printf("  Last scan:          %s\n", status.ScannedAt)
		fmt.Printf("  Directories scanned: %d\n", status.ScannedDirs)
		if status.Error != "" {
			fmt.Printf("  Last error:         %s\n", status.Error)
		}
	}
}

// runDaemon is the main loop of the backgrounded daemon process.
func runDaemon(rootPath string) {
	pidFilePath, err := daemon.PIDPath()
	if err != nil {
		log.Fatalf("resolve pid path: %v", err)
	}

	// Write own PID — defer cleanup for crash safety.
	ownPID := os.Getpid()
	if err := daemon.WritePID(pidFilePath, ownPID); err != nil {
		log.Fatalf("write pid file: %v", err)
	}
	defer func() {
		_ = daemon.RemovePID(pidFilePath)
		log.Printf("daemon: pid file cleaned up")
	}()

	log.Printf("daemon: started (PID: %d), scanning %s", ownPID, rootPath)

	// Open database.
	dbPath, err := daemon.DBPath()
	if err != nil {
		log.Fatalf("resolve db path: %v", err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() {
		database.Close()
		log.Printf("daemon: database closed")
	}()

	// Set up signal handling.
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-sigCh
		log.Printf("daemon: received %s, shutting down", sig)
		cancel()
		close(done)
	}()

	// Run scan.
	s := scanner.New(database, scanner.DefaultConfig())
	scanErr := s.Scan(ctx, rootPath)

	// Write status.
	statusFilePath, err := daemon.StatusPath()
	if err != nil {
		log.Printf("daemon: resolve status path: %v", err)
	} else {
		status := daemon.Status{
			PID:      int64(ownPID),
			Running:  true,
			RootPath: rootPath,
		}

		if scanErr != nil {
			status.Error = scanErr.Error()
			log.Printf("daemon: scan error: %v", scanErr)
		} else {
			count, countErr := database.CountDirs(context.Background())
			if countErr != nil {
				log.Printf("daemon: count dirs error: %v", countErr)
			} else {
				status.ScannedDirs = count
			}
			status.ScannedAt = time.Now().UTC().Format(time.RFC3339)
			log.Printf("daemon: scan complete, %d directories", status.ScannedDirs)
		}

		if err := daemon.WriteStatus(statusFilePath, status); err != nil {
			log.Printf("daemon: write status file: %v", err)
		}
	}

	// Start HTTP server — blocks until done closes (signal triggers graceful shutdown).
	srv := server.New(database, server.DefaultPort)
	log.Printf("daemon: http server listening on :%d", server.DefaultPort)
	if err := srv.ListenAndServe(done); err != nil && err != http.ErrServerClosed {
		log.Printf("daemon: http server error: %v", err)
	}
	log.Printf("daemon: http server stopped")
}
