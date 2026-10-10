// Package logging emits live run events as NDJSON (one JSON object per line).
// It is safe for concurrent use: parallel case workers share one Emitter and
// writes are serialized.
package logging

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
)

// Emitter writes NDJSON event lines to w.
type Emitter struct {
	mu sync.Mutex
	w  io.Writer
}

// New creates an emitter writing to w.
func New(w io.Writer) *Emitter { return &Emitter{w: w} }

type envelope struct {
	TS        string         `json:"ts"`
	Level     string         `json:"level"`
	Event     string         `json:"event"`
	Env       string         `json:"env,omitempty"`
	Task      string         `json:"task,omitempty"`
	Installer string         `json:"installer,omitempty"`
	Data      map[string]any `json:"data"`
}

const tsFormat = "2006-01-02T15:04:05.000Z07:00"

func (e *Emitter) write(level, event, envName, taskName, installer string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	line, err := json.Marshal(envelope{
		TS:        time.Now().UTC().Format(tsFormat),
		Level:     level,
		Event:     event,
		Env:       envName,
		Task:      taskName,
		Installer: installer,
		Data:      data,
	})
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.w.Write(append(line, '\n'))
}

// RunStart announces the run: number of configs, planned cases and parallel limit.
func (e *Emitter) RunStart(configs, cases, parallel int) {
	e.write("info", "run_start", "", "", "", map[string]any{
		"configs": configs, "cases": cases, "parallel": parallel,
	})
}

// RunEnd closes the run with aggregated case totals.
func (e *Emitter) RunEnd(passed, failed, configErrors int) {
	data := map[string]any{"passed": passed, "failed": failed}
	if configErrors > 0 {
		data["config_errors"] = configErrors
	}
	e.write("info", "run_end", "", "", "", data)
}

// Error reports an infrastructure error outside a case context.
func (e *Emitter) Error(message string) {
	e.write("error", "error", "", "", "", map[string]any{"message": message})
}

// EnvStart announces the first started case of an environment.
func (e *Emitter) EnvStart(name, image, workdir string) {
	e.write("info", "env_start", name, "", "", map[string]any{
		"image": image, "workdir": workdir,
	})
}

// EnvEnd closes an environment: emitted when its last case finished.
func (e *Emitter) EnvEnd(name string, passed, failed int) {
	e.write("info", "env_end", name, "", "", map[string]any{
		"passed": passed, "failed": failed,
	})
}

// CaseLogger emits the events of one (environment, task, installer) case.
type CaseLogger struct {
	e                      *Emitter
	env, task, installerID string
}

// Case binds a case context (env / task / installer) to the emitter.
func (e *Emitter) Case(envName, taskName, installer string) *CaseLogger {
	return &CaseLogger{e: e, env: envName, task: taskName, installerID: installer}
}

// CaseStart logs the case container creation.
func (c *CaseLogger) CaseStart(containerID, containerName string) {
	c.e.write("info", "case_start", c.env, c.task, c.installerID, map[string]any{
		"container_id": containerID, "container_name": containerName,
	})
}

// CaseEnd logs the case result after the teardown decision. kept is only
// true when --keep is active and the case failed (container stays up).
func (c *CaseLogger) CaseEnd(containerID string, pass bool, passed, failed int, durationMs int64, kept bool) {
	level := "info"
	if !pass {
		level = "error"
	}
	data := map[string]any{
		"container_id": containerID,
		"pass":         pass,
		"passed":       passed,
		"failed":       failed,
		"duration_ms":  durationMs,
	}
	if kept {
		data["kept"] = true
	}
	c.e.write(level, "case_end", c.env, c.task, c.installerID, data)
}

// Step logs one executed phase step: copy / setup-install / install / uninstall.
func (c *CaseLogger) Step(kind, command string, exitCode int, pass bool, output string) {
	level := "info"
	if !pass {
		level = "error"
	}
	data := map[string]any{"kind": kind, "exit_code": exitCode, "pass": pass}
	if command != "" {
		data["command"] = command
	}
	if output != "" {
		data["output"] = truncate(output)
	}
	c.e.write(level, "step", c.env, c.task, c.installerID, data)
}

// Check logs one check result; fields carry kind-specific extras
// (exit_code, expected_exit_code, expected_stdout_contains, detail, output).
func (c *CaseLogger) Check(kind, name string, pass bool, fields map[string]any) {
	level := "info"
	if !pass {
		level = "error"
	}
	data := map[string]any{"kind": kind, "name": name, "pass": pass}
	for k, v := range fields {
		data[k] = v
	}
	c.e.write(level, "check", c.env, c.task, c.installerID, data)
}

// Error logs an infrastructure error within the case context.
func (c *CaseLogger) Error(message string) {
	c.e.write("error", "error", c.env, c.task, c.installerID, map[string]any{"message": message})
}

func truncate(s string) string {
	s = strings.TrimSpace(s)
	const max = 2000
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
