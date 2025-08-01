//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/creack/pty"
	"github.com/moby/moby/v2/integration-cli/cli"
	"github.com/moby/moby/v2/integration-cli/cli/build"
	"golang.org/x/sys/unix"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
	"gotest.tools/v3/skip"
)

// #5979
func (s *DockerCLIEventSuite) TestEventsRedirectStdout(c *testing.T) {
	since := daemonUnixTime(c)
	cli.DockerCmd(c, "run", "busybox", "true")

	file, err := os.CreateTemp("", "")
	assert.NilError(c, err, "could not create temp file")
	defer os.Remove(file.Name())

	command := fmt.Sprintf("%s events --since=%s --until=%s > %s", dockerBinary, since, daemonUnixTime(c), file.Name())
	_, tty, err := pty.Open()
	assert.NilError(c, err, "Could not open pty")
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	assert.NilError(c, cmd.Run(), "run err for command %q", command)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		for _, ch := range scanner.Text() {
			assert.Check(c, unicode.IsControl(ch) == false, "found control character %v", []byte(string(ch)))
		}
	}
	assert.NilError(c, scanner.Err(), "Scan err for command %q", command)
}

func (s *DockerCLIEventSuite) TestEventsOOMDisableFalse(c *testing.T) {
	testRequires(c, DaemonIsLinux, oomControl, memoryLimitSupport, swapMemorySupport, NotPpc64le)
	skip.If(c, GitHubActions, "FIXME: https://github.com/moby/moby/pull/36541")

	errChan := make(chan error, 1)
	go func() {
		defer close(errChan)
		out, exitCode, _ := dockerCmdWithError("run", "--name", "oomFalse", "-m", "10MB", "busybox", "sh", "-c", "x=a; while true; do x=$x$x$x$x; done")
		if expected := 137; exitCode != expected {
			errChan <- fmt.Errorf("wrong exit code for OOM container: expected %d, got %d (output: %q)", expected, exitCode, out)
		}
	}()
	select {
	case err := <-errChan:
		assert.NilError(c, err)
	case <-time.After(30 * time.Second):
		c.Fatal("Timeout waiting for container to die on OOM")
	}

	out := cli.DockerCmd(c, "events", "--since=0", "-f", "container=oomFalse", "--until", daemonUnixTime(c)).Stdout()
	events := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	nEvents := len(events)

	assert.Assert(c, nEvents >= 5)
	assert.Equal(c, parseEventAction(c, events[nEvents-5]), "create")
	assert.Equal(c, parseEventAction(c, events[nEvents-4]), "attach")
	assert.Equal(c, parseEventAction(c, events[nEvents-3]), "start")
	assert.Equal(c, parseEventAction(c, events[nEvents-2]), "oom")
	assert.Equal(c, parseEventAction(c, events[nEvents-1]), "die")
}

func (s *DockerCLIEventSuite) TestEventsOOMDisableTrue(c *testing.T) {
	testRequires(c, DaemonIsLinux, oomControl, memoryLimitSupport, swapMemorySupport, NotPpc64le)
	skip.If(c, GitHubActions, "FIXME: https://github.com/moby/moby/pull/36541")

	errChan := make(chan error, 1)
	observer, err := newEventObserver(c)
	assert.NilError(c, err)
	err = observer.Start()
	assert.NilError(c, err)
	defer observer.Stop()

	go func() {
		defer close(errChan)
		out, exitCode, _ := dockerCmdWithError("run", "--oom-kill-disable=true", "--name", "oomTrue", "-m", "10MB", "busybox", "sh", "-c", "x=a; while true; do x=$x$x$x$x; done")
		if expected := 137; exitCode != expected {
			errChan <- fmt.Errorf("wrong exit code for OOM container: expected %d, got %d (output: %q)", expected, exitCode, out)
		}
	}()

	cli.WaitRun(c, "oomTrue")
	defer cli.Docker(cli.Args("kill", "oomTrue"))
	containerID := inspectField(c, "oomTrue", "Id")

	testActions := map[string]chan bool{
		"oom": make(chan bool),
	}

	matcher := matchEventLine(containerID, "container", testActions)
	processor := processEventMatch(testActions)
	go observer.Match(matcher, processor)

	select {
	case <-time.After(20 * time.Second):
		observer.CheckEventError(c, containerID, "oom", matcher)
	case <-testActions["oom"]:
	// ignore, done
	case errRun := <-errChan:
		if errRun != nil {
			c.Fatalf("%v", errRun)
		} else {
			c.Fatalf("container should be still running but it's not")
		}
	}

	status := inspectField(c, "oomTrue", "State.Status")
	assert.Equal(c, strings.TrimSpace(status), "running", "container should be still running")
}

// #18453
func (s *DockerCLIEventSuite) TestEventsContainerFilterByName(c *testing.T) {
	testRequires(c, DaemonIsLinux)
	cOut := cli.DockerCmd(c, "run", "--name=foo", "-d", "busybox", "top").Stdout()
	c1 := strings.TrimSpace(cOut)
	cli.WaitRun(c, "foo")
	cOut = cli.DockerCmd(c, "run", "--name=bar", "-d", "busybox", "top").Stdout()
	c2 := strings.TrimSpace(cOut)
	cli.WaitRun(c, "bar")
	out := cli.DockerCmd(c, "events", "-f", "container=foo", "--since=0", "--until", daemonUnixTime(c)).Stdout()
	assert.Assert(c, strings.Contains(out, c1), out)
	assert.Assert(c, !strings.Contains(out, c2), out)
}

// #18453
func (s *DockerCLIEventSuite) TestEventsContainerFilterBeforeCreate(c *testing.T) {
	testRequires(c, DaemonIsLinux)
	buf := &bytes.Buffer{}
	cmd := exec.Command(dockerBinary, "events", "-f", "container=foo", "--since=0")
	cmd.Stdout = buf
	assert.NilError(c, cmd.Start())
	defer cmd.Wait()
	defer cmd.Process.Kill()

	// Sleep for a second to make sure we are testing the case where events are listened before container starts.
	time.Sleep(time.Second)
	id := cli.DockerCmd(c, "run", "--name=foo", "-d", "busybox", "top").Stdout()
	cID := strings.TrimSpace(id)
	for i := 0; ; i++ {
		out := buf.String()
		if strings.Contains(out, cID) {
			break
		}
		if i > 30 {
			c.Fatalf("Missing event of container (foo, %v), got %q", cID, out)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (s *DockerCLIEventSuite) TestVolumeEvents(c *testing.T) {
	testRequires(c, DaemonIsLinux)

	since := daemonUnixTime(c)

	// Observe create/mount volume actions
	cli.DockerCmd(c, "volume", "create", "test-event-volume-local")
	cli.DockerCmd(c, "run", "--name", "test-volume-container", "--volume", "test-event-volume-local:/foo", "-d", "busybox", "true")

	// Observe unmount/destroy volume actions
	cli.DockerCmd(c, "rm", "-f", "test-volume-container")
	cli.DockerCmd(c, "volume", "rm", "test-event-volume-local")

	until := daemonUnixTime(c)
	out := cli.DockerCmd(c, "events", "--since", since, "--until", until).Stdout()
	events := strings.Split(strings.TrimSpace(out), "\n")
	assert.Assert(c, len(events) > 3)

	volumeEvents := eventActionsByIDAndType(c, events, "test-event-volume-local", "volume")
	assert.Equal(c, len(volumeEvents), 4)
	assert.Equal(c, volumeEvents[0], "create")
	assert.Equal(c, volumeEvents[1], "mount")
	assert.Equal(c, volumeEvents[2], "unmount")
	assert.Equal(c, volumeEvents[3], "destroy")
}

func (s *DockerCLIEventSuite) TestNetworkEvents(c *testing.T) {
	testRequires(c, DaemonIsLinux)

	since := daemonUnixTime(c)

	// Observe create/connect network actions
	cli.DockerCmd(c, "network", "create", "test-event-network-local")
	cli.DockerCmd(c, "run", "--name", "test-network-container", "--net", "test-event-network-local", "-d", "busybox", "true")

	// Observe disconnect/destroy network actions
	cli.DockerCmd(c, "rm", "-f", "test-network-container")
	cli.DockerCmd(c, "network", "rm", "test-event-network-local")

	until := daemonUnixTime(c)
	out := cli.DockerCmd(c, "events", "--since", since, "--until", until).Stdout()
	events := strings.Split(strings.TrimSpace(out), "\n")
	assert.Assert(c, len(events) > 4)

	netEvents := eventActionsByIDAndType(c, events, "test-event-network-local", "network")
	assert.Equal(c, len(netEvents), 4)
	assert.Equal(c, netEvents[0], "create")
	assert.Equal(c, netEvents[1], "connect")
	assert.Equal(c, netEvents[2], "disconnect")
	assert.Equal(c, netEvents[3], "destroy")
}

func (s *DockerCLIEventSuite) TestEventsContainerWithMultiNetwork(c *testing.T) {
	testRequires(c, DaemonIsLinux)

	// Observe create/connect network actions
	cli.DockerCmd(c, "network", "create", "test-event-network-local-1")
	cli.DockerCmd(c, "network", "create", "test-event-network-local-2")
	cli.DockerCmd(c, "run", "--name", "test-network-container", "--net", "test-event-network-local-1", "-td", "busybox", "sh")
	cli.WaitRun(c, "test-network-container")
	cli.DockerCmd(c, "network", "connect", "test-event-network-local-2", "test-network-container")

	since := daemonUnixTime(c)

	cli.DockerCmd(c, "stop", "-t", "1", "test-network-container")

	until := daemonUnixTime(c)
	out := cli.DockerCmd(c, "events", "--since", since, "--until", until, "-f", "type=network").Stdout()
	netEvents := strings.Split(strings.TrimSpace(out), "\n")

	// received two network disconnect events
	assert.Equal(c, len(netEvents), 2)
	assert.Assert(c, is.Contains(netEvents[0], "disconnect"))
	assert.Assert(c, is.Contains(netEvents[1], "disconnect"))

	// both networks appeared in the network event output
	assert.Assert(c, is.Contains(out, "test-event-network-local-1"))
	assert.Assert(c, is.Contains(out, "test-event-network-local-2"))
}

func (s *DockerCLIEventSuite) TestEventsStreaming(c *testing.T) {
	testRequires(c, DaemonIsLinux)

	observer, err := newEventObserver(c)
	assert.NilError(c, err)
	err = observer.Start()
	assert.NilError(c, err)
	defer observer.Stop()

	out := cli.DockerCmd(c, "run", "-d", "busybox:latest", "true").Stdout()
	containerID := strings.TrimSpace(out)

	testActions := map[string]chan bool{
		"create":  make(chan bool, 1),
		"start":   make(chan bool, 1),
		"die":     make(chan bool, 1),
		"destroy": make(chan bool, 1),
	}

	matcher := matchEventLine(containerID, "container", testActions)
	processor := processEventMatch(testActions)
	go observer.Match(matcher, processor)

	select {
	case <-time.After(5 * time.Second):
		observer.CheckEventError(c, containerID, "create", matcher)
	case <-testActions["create"]:
		// ignore, done
	}

	select {
	case <-time.After(5 * time.Second):
		observer.CheckEventError(c, containerID, "start", matcher)
	case <-testActions["start"]:
		// ignore, done
	}

	select {
	case <-time.After(5 * time.Second):
		observer.CheckEventError(c, containerID, "die", matcher)
	case <-testActions["die"]:
		// ignore, done
	}

	cli.DockerCmd(c, "rm", containerID)

	select {
	case <-time.After(5 * time.Second):
		observer.CheckEventError(c, containerID, "destroy", matcher)
	case <-testActions["destroy"]:
		// ignore, done
	}
}

func (s *DockerCLIEventSuite) TestEventsImageUntagDelete(c *testing.T) {
	testRequires(c, DaemonIsLinux)

	observer, err := newEventObserver(c)
	assert.NilError(c, err)
	err = observer.Start()
	assert.NilError(c, err)
	defer observer.Stop()

	name := "testimageevents"
	buildImageSuccessfully(c, name, build.WithDockerfile(`FROM scratch
		MAINTAINER "docker"`))
	imageID := getIDByName(c, name)
	assert.NilError(c, deleteImages(name))

	testActions := map[string]chan bool{
		"untag":  make(chan bool, 1),
		"delete": make(chan bool, 1),
	}

	matcher := matchEventLine(imageID, "image", testActions)
	processor := processEventMatch(testActions)
	go observer.Match(matcher, processor)

	select {
	case <-time.After(10 * time.Second):
		observer.CheckEventError(c, imageID, "untag", matcher)
	case <-testActions["untag"]:
		// ignore, done
	}

	select {
	case <-time.After(10 * time.Second):
		observer.CheckEventError(c, imageID, "delete", matcher)
	case <-testActions["delete"]:
		// ignore, done
	}
}

func (s *DockerCLIEventSuite) TestEventsFilterVolumeAndNetworkType(c *testing.T) {
	testRequires(c, DaemonIsLinux)

	since := daemonUnixTime(c)

	cli.DockerCmd(c, "network", "create", "test-event-network-type")
	cli.DockerCmd(c, "volume", "create", "test-event-volume-type")

	out := cli.DockerCmd(c, "events", "--filter", "type=volume", "--filter", "type=network", "--since", since, "--until", daemonUnixTime(c)).Stdout()
	events := strings.Split(strings.TrimSpace(out), "\n")
	assert.Assert(c, len(events) >= 2, out)

	networkActions := eventActionsByIDAndType(c, events, "test-event-network-type", "network")
	volumeActions := eventActionsByIDAndType(c, events, "test-event-volume-type", "volume")

	assert.Equal(c, volumeActions[0], "create")
	assert.Equal(c, networkActions[0], "create")
}

func (s *DockerCLIEventSuite) TestEventsFilterVolumeID(c *testing.T) {
	testRequires(c, DaemonIsLinux)

	since := daemonUnixTime(c)

	cli.DockerCmd(c, "volume", "create", "test-event-volume-id")
	out := cli.DockerCmd(c, "events", "--filter", "volume=test-event-volume-id", "--since", since, "--until", daemonUnixTime(c)).Stdout()
	events := strings.Split(strings.TrimSpace(out), "\n")
	assert.Equal(c, len(events), 1)

	assert.Equal(c, len(events), 1)
	assert.Assert(c, is.Contains(events[0], "test-event-volume-id"))
	assert.Assert(c, is.Contains(events[0], "driver=local"))
}

func (s *DockerCLIEventSuite) TestEventsFilterNetworkID(c *testing.T) {
	testRequires(c, DaemonIsLinux)

	since := daemonUnixTime(c)

	cli.DockerCmd(c, "network", "create", "test-event-network-local")
	out := cli.DockerCmd(c, "events", "--filter", "network=test-event-network-local", "--since", since, "--until", daemonUnixTime(c)).Stdout()
	events := strings.Split(strings.TrimSpace(out), "\n")
	assert.Equal(c, len(events), 1)
	assert.Assert(c, is.Contains(events[0], "test-event-network-local"))
	assert.Assert(c, is.Contains(events[0], "type=bridge"))
}

func (s *DockerDaemonSuite) TestDaemonEvents(c *testing.T) {
	// daemon config file
	configFilePath := "test.json"
	defer os.Remove(configFilePath)

	daemonConfig := `{"labels":["foo=bar"]}`
	err := os.WriteFile(configFilePath, []byte(daemonConfig), 0o644)
	assert.NilError(c, err)
	s.d.Start(c, "--config-file="+configFilePath)

	info := s.d.Info(c)

	daemonConfig = `{"max-concurrent-downloads":1,"labels":["bar=foo"], "shutdown-timeout": 10}`
	err = os.WriteFile(configFilePath, []byte(daemonConfig), 0o644)
	assert.NilError(c, err)

	assert.NilError(c, s.d.Signal(unix.SIGHUP))
	time.Sleep(3 * time.Second)

	out, err := s.d.Cmd("events", "--since=0", "--until", daemonUnixTime(c))
	assert.NilError(c, err)

	// only check for values known (daemon ID/name) or explicitly set above,
	// otherwise just check for names being present.
	expectedSubstrings := []string{
		` daemon reload ` + info.ID + " ",
		`debug=true, `,
		` default-ipc-mode=`,
		` default-runtime=`,
		` default-shm-size=`,
		` insecure-registries=[`,
		` labels=["bar=foo"], `,
		` live-restore=`,
		` max-concurrent-downloads=1, `,
		` max-concurrent-uploads=5, `,
		` name=` + info.Name,
		` registry-mirrors=[`,
		` runtimes=`,
		` shutdown-timeout=10)`,
	}

	for _, s := range expectedSubstrings {
		assert.Check(c, is.Contains(out, s))
	}
}

func (s *DockerDaemonSuite) TestDaemonEventsWithFilters(c *testing.T) {
	// daemon config file
	configFilePath := "test.json"
	defer os.Remove(configFilePath)

	daemonConfig := `{"labels":["foo=bar"]}`
	err := os.WriteFile(configFilePath, []byte(daemonConfig), 0o644)
	assert.NilError(c, err)
	s.d.Start(c, "--config-file="+configFilePath)

	info := s.d.Info(c)

	assert.NilError(c, s.d.Signal(unix.SIGHUP))
	time.Sleep(3 * time.Second)

	out, err := s.d.Cmd("events", "--since=0", "--until", daemonUnixTime(c), "--filter", fmt.Sprintf("daemon=%s", info.ID))
	assert.NilError(c, err)
	assert.Assert(c, is.Contains(out, fmt.Sprintf("daemon reload %s", info.ID)))

	out, err = s.d.Cmd("events", "--since=0", "--until", daemonUnixTime(c), "--filter", fmt.Sprintf("daemon=%s", info.ID))
	assert.NilError(c, err)
	assert.Assert(c, is.Contains(out, fmt.Sprintf("daemon reload %s", info.ID)))

	out, err = s.d.Cmd("events", "--since=0", "--until", daemonUnixTime(c), "--filter", "daemon=foo")
	assert.NilError(c, err)
	assert.Assert(c, !strings.Contains(out, fmt.Sprintf("daemon reload %s", info.ID)))

	out, err = s.d.Cmd("events", "--since=0", "--until", daemonUnixTime(c), "--filter", "type=daemon")
	assert.NilError(c, err)
	assert.Assert(c, is.Contains(out, fmt.Sprintf("daemon reload %s", info.ID)))

	out, err = s.d.Cmd("events", "--since=0", "--until", daemonUnixTime(c), "--filter", "type=container")
	assert.NilError(c, err)
	assert.Assert(c, !strings.Contains(out, fmt.Sprintf("daemon reload %s", info.ID)))
}
