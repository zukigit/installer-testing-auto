// Package env defines the Environment abstraction. Every environment type
// (docker today, winrm later) registers a factory here and implements the
// interface; tasks and the runner never depend on a concrete implementation.
package env

import (
	"context"
	"fmt"
	"sort"
)

const (
	// TypeDocker is the container-based environment (testcontainers-go).
	TypeDocker = "docker"
)

// Environment is one provisioned target (container, VM, remote host...).
type Environment interface {
	Name() string
	// Start provisions the environment.
	Start(ctx context.Context) error
	// Copy copies a local file into the environment at dest.
	Copy(ctx context.Context, src, dest string) error
	// Exec runs a command, returns stdout, stderr and the exit code.
	Exec(ctx context.Context, cmd []string) (stdout, stderr string, exitCode int, err error)
	// Stop tears the environment down.
	Stop(ctx context.Context) error
}

// Factory builds an unstarted Environment for a registered type.
type Factory func(name, image, workdir string) (Environment, error)

var factories = map[string]Factory{}

// Register adds an environment type factory. Called from the concrete
// implementation files in this package (e.g. docker.go).
func Register(envType string, f Factory) {
	factories[envType] = f
}

// New builds an unstarted environment of the given type.
func New(envType, name, image, workdir string) (Environment, error) {
	f, ok := factories[envType]
	if !ok {
		return nil, fmt.Errorf("unknown environment type %q (supported: %v)", envType, SupportedTypes())
	}
	return f(name, image, workdir)
}

// SupportedTypes lists the registered environment types.
func SupportedTypes() []string {
	types := make([]string, 0, len(factories))
	for t := range factories {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}
