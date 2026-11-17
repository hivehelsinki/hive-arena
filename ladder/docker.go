package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Label set on every Docker object created by the ladder, so that leftovers
// can be found and removed
const DockerLabel = "hive-ladder"

// docker runs a docker CLI command and returns its trimmed standard output
func docker(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// RemoveLeftovers removes all containers and networks created by the ladder,
// for instance after a crash of the ladder itself
func RemoveLeftovers(ctx context.Context) error {
	containers, err := docker(ctx, "ps", "-aq", "--filter", "label="+DockerLabel)
	if err != nil {
		return err
	}
	if containers != "" {
		if _, err := docker(ctx, append([]string{"rm", "-f"}, strings.Fields(containers)...)...); err != nil {
			return err
		}
	}

	networks, err := docker(ctx, "network", "ls", "-q", "--filter", "label="+DockerLabel)
	if err != nil {
		return err
	}
	if networks != "" {
		if _, err := docker(ctx, append([]string{"network", "rm"}, strings.Fields(networks)...)...); err != nil {
			return err
		}
	}
	return nil
}

// dockerCombined runs a docker CLI command and returns its combined output
func dockerCombined(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return string(out), err
}

// dockerAvailable reports whether the docker daemon answers
func dockerAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := docker(ctx, "version", "--format", "{{.Server.Version}}")
	return err == nil
}
