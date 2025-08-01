package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/filters"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/v2/integration-cli/cli"
	"github.com/moby/moby/v2/testutil"
	"github.com/moby/moby/v2/testutil/request"
	"gotest.tools/v3/assert"
)

func (s *DockerAPISuite) TestAPINetworkGetDefaults(c *testing.T) {
	testRequires(c, DaemonIsLinux)
	// By default docker daemon creates 3 networks. check if they are present
	defaults := []string{"bridge", "host", "none"}
	for _, nn := range defaults {
		assert.Assert(c, isNetworkAvailable(c, nn))
	}
}

func (s *DockerAPISuite) TestAPINetworkFilter(c *testing.T) {
	testRequires(c, DaemonIsLinux)
	nr := getNetworkResource(c, getNetworkIDByName(c, "bridge"))
	assert.Equal(c, nr.Name, "bridge")
}

func (s *DockerAPISuite) TestAPINetworkInspectBridge(c *testing.T) {
	testRequires(c, DaemonIsLinux)
	// Inspect default bridge network
	nr := getNetworkResource(c, "bridge")
	assert.Equal(c, nr.Name, "bridge")

	// run a container and attach it to the default bridge network
	out := cli.DockerCmd(c, "run", "-d", "--name", "test", "busybox", "top").Stdout()
	containerID := strings.TrimSpace(out)
	containerIP := findContainerIP(c, "test", "bridge")

	// inspect default bridge network again and make sure the container is connected
	nr = getNetworkResource(c, nr.ID)
	assert.Equal(c, nr.Driver, "bridge")
	assert.Equal(c, nr.Scope, "local")
	assert.Equal(c, nr.Internal, false)
	assert.Equal(c, nr.EnableIPv6, false)
	assert.Equal(c, nr.IPAM.Driver, "default")
	_, ok := nr.Containers[containerID]
	assert.Assert(c, ok)

	ip, _, err := net.ParseCIDR(nr.Containers[containerID].IPv4Address)
	assert.NilError(c, err)
	assert.Equal(c, ip.String(), containerIP)
}

func (s *DockerAPISuite) TestAPINetworkInspectUserDefinedNetwork(c *testing.T) {
	testRequires(c, DaemonIsLinux)
	// IPAM configuration inspect
	ipam := &network.IPAM{
		Driver: "default",
		Config: []network.IPAMConfig{{Subnet: "172.28.0.0/16", IPRange: "172.28.5.0/24", Gateway: "172.28.5.254"}},
	}
	config := network.CreateRequest{
		Name: "br0",
		CreateOptions: network.CreateOptions{
			Driver:  "bridge",
			IPAM:    ipam,
			Options: map[string]string{"foo": "bar", "opts": "dopts"},
		},
	}
	id0 := createNetwork(c, config, http.StatusCreated)
	assert.Assert(c, isNetworkAvailable(c, "br0"))

	nr := getNetworkResource(c, id0)
	assert.Equal(c, len(nr.IPAM.Config), 1)
	assert.Equal(c, nr.IPAM.Config[0].Subnet, "172.28.0.0/16")
	assert.Equal(c, nr.IPAM.Config[0].IPRange, "172.28.5.0/24")
	assert.Equal(c, nr.IPAM.Config[0].Gateway, "172.28.5.254")
	assert.Equal(c, nr.Options["foo"], "bar")
	assert.Equal(c, nr.Options["opts"], "dopts")

	// delete the network and make sure it is deleted
	deleteNetwork(c, id0, true)
	assert.Assert(c, !isNetworkAvailable(c, "br0"))
}

func (s *DockerAPISuite) TestAPINetworkConnectDisconnect(c *testing.T) {
	testRequires(c, DaemonIsLinux)
	// Create test network
	name := "testnetwork"
	config := network.CreateRequest{
		Name: name,
	}
	id := createNetwork(c, config, http.StatusCreated)
	nr := getNetworkResource(c, id)
	assert.Equal(c, nr.Name, name)
	assert.Equal(c, nr.ID, id)
	assert.Equal(c, len(nr.Containers), 0)

	// run a container
	out := cli.DockerCmd(c, "run", "-d", "--name", "test", "busybox", "top").Stdout()
	containerID := strings.TrimSpace(out)

	// connect the container to the test network
	connectNetwork(c, nr.ID, containerID)

	// inspect the network to make sure container is connected
	nr = getNetworkResource(c, nr.ID)
	assert.Equal(c, len(nr.Containers), 1)
	_, ok := nr.Containers[containerID]
	assert.Assert(c, ok)

	// check if container IP matches network inspect
	ip, _, err := net.ParseCIDR(nr.Containers[containerID].IPv4Address)
	assert.NilError(c, err)
	containerIP := findContainerIP(c, "test", "testnetwork")
	assert.Equal(c, ip.String(), containerIP)

	// disconnect container from the network
	disconnectNetwork(c, nr.ID, containerID)
	nr = getNetworkResource(c, nr.ID)
	assert.Equal(c, nr.Name, name)
	assert.Equal(c, len(nr.Containers), 0)

	// delete the network
	deleteNetwork(c, nr.ID, true)
}

func (s *DockerAPISuite) TestAPINetworkIPAMMultipleBridgeNetworks(c *testing.T) {
	testRequires(c, DaemonIsLinux)
	// test0 bridge network
	ipam0 := &network.IPAM{
		Driver: "default",
		Config: []network.IPAMConfig{{Subnet: "192.178.0.0/16", IPRange: "192.178.128.0/17", Gateway: "192.178.138.100"}},
	}
	config0 := network.CreateRequest{
		Name: "test0",
		CreateOptions: network.CreateOptions{
			Driver: "bridge",
			IPAM:   ipam0,
		},
	}
	id0 := createNetwork(c, config0, http.StatusCreated)
	assert.Assert(c, isNetworkAvailable(c, "test0"))

	ipam1 := &network.IPAM{
		Driver: "default",
		Config: []network.IPAMConfig{{Subnet: "192.178.128.0/17", Gateway: "192.178.128.1"}},
	}
	// test1 bridge network overlaps with test0
	config1 := network.CreateRequest{
		Name: "test1",
		CreateOptions: network.CreateOptions{
			Driver: "bridge",
			IPAM:   ipam1,
		},
	}
	createNetwork(c, config1, http.StatusForbidden)
	assert.Assert(c, !isNetworkAvailable(c, "test1"))

	ipam2 := &network.IPAM{
		Driver: "default",
		Config: []network.IPAMConfig{{Subnet: "192.169.0.0/16", Gateway: "192.169.100.100"}},
	}
	// test2 bridge network does not overlap
	config2 := network.CreateRequest{
		Name: "test2",
		CreateOptions: network.CreateOptions{
			Driver: "bridge",
			IPAM:   ipam2,
		},
	}
	createNetwork(c, config2, http.StatusCreated)
	assert.Assert(c, isNetworkAvailable(c, "test2"))

	// remove test0 and retry to create test1
	deleteNetwork(c, id0, true)
	createNetwork(c, config1, http.StatusCreated)
	assert.Assert(c, isNetworkAvailable(c, "test1"))

	// for networks w/o ipam specified, docker will choose proper non-overlapping subnets
	createNetwork(c, network.CreateRequest{Name: "test3"}, http.StatusCreated)
	assert.Assert(c, isNetworkAvailable(c, "test3"))
	createNetwork(c, network.CreateRequest{Name: "test4"}, http.StatusCreated)
	assert.Assert(c, isNetworkAvailable(c, "test4"))
	createNetwork(c, network.CreateRequest{Name: "test5"}, http.StatusCreated)
	assert.Assert(c, isNetworkAvailable(c, "test5"))

	for i := 1; i < 6; i++ {
		deleteNetwork(c, fmt.Sprintf("test%d", i), true)
	}
}

func (s *DockerAPISuite) TestAPICreateDeletePredefinedNetworks(c *testing.T) {
	testRequires(c, DaemonIsLinux, SwarmInactive)
	createDeletePredefinedNetwork(c, "bridge")
	createDeletePredefinedNetwork(c, "none")
	createDeletePredefinedNetwork(c, "host")
}

func createDeletePredefinedNetwork(t *testing.T, name string) {
	// Create pre-defined network
	config := network.CreateRequest{Name: name}
	expectedStatus := http.StatusForbidden
	createNetwork(t, config, expectedStatus)
	deleteNetwork(t, name, false)
}

func isNetworkAvailable(t *testing.T, name string) bool {
	resp, body, err := request.Get(testutil.GetContext(t), "/networks")
	assert.NilError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, resp.StatusCode, http.StatusOK)

	var nJSON []network.Inspect
	err = json.NewDecoder(body).Decode(&nJSON)
	assert.NilError(t, err)

	for _, n := range nJSON {
		if n.Name == name {
			return true
		}
	}
	return false
}

func getNetworkIDByName(t *testing.T, name string) string {
	filterJSON, err := filters.ToJSON(filters.NewArgs(filters.Arg("name", name)))
	assert.NilError(t, err)
	v := url.Values{}
	v.Set("filters", filterJSON)

	resp, body, err := request.Get(testutil.GetContext(t), "/networks?"+v.Encode())
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.NilError(t, err)

	var nJSON []network.Inspect
	err = json.NewDecoder(body).Decode(&nJSON)
	assert.NilError(t, err)
	var res string
	for _, n := range nJSON {
		// Find exact match
		if n.Name == name {
			res = n.ID
		}
	}
	assert.Assert(t, res != "")

	return res
}

func getNetworkResource(t *testing.T, id string) *network.Inspect {
	_, obj, err := request.Get(testutil.GetContext(t), "/networks/"+id)
	assert.NilError(t, err)

	nr := network.Inspect{}
	err = json.NewDecoder(obj).Decode(&nr)
	assert.NilError(t, err)

	return &nr
}

func createNetwork(t *testing.T, config network.CreateRequest, expectedStatusCode int) string {
	t.Helper()

	resp, body, err := request.Post(testutil.GetContext(t), "/networks/create", request.JSONBody(config))
	assert.NilError(t, err)
	defer resp.Body.Close()

	if expectedStatusCode >= 0 {
		assert.Equal(t, resp.StatusCode, expectedStatusCode)
	} else {
		assert.Assert(t, resp.StatusCode != -expectedStatusCode)
	}

	if expectedStatusCode == http.StatusCreated || expectedStatusCode < 0 {
		var nr network.CreateResponse
		err = json.NewDecoder(body).Decode(&nr)
		assert.NilError(t, err)

		return nr.ID
	}
	return ""
}

func connectNetwork(t *testing.T, nid, cid string) {
	resp, _, err := request.Post(testutil.GetContext(t), "/networks/"+nid+"/connect", request.JSONBody(network.ConnectOptions{
		Container: cid,
	}))
	assert.NilError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)
}

func disconnectNetwork(t *testing.T, nid, cid string) {
	config := network.ConnectOptions{
		Container: cid,
	}

	resp, _, err := request.Post(testutil.GetContext(t), "/networks/"+nid+"/disconnect", request.JSONBody(config))
	assert.NilError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)
}

func deleteNetwork(t *testing.T, id string, shouldSucceed bool) {
	resp, _, err := request.Delete(testutil.GetContext(t), "/networks/"+id)
	assert.NilError(t, err)
	defer resp.Body.Close()
	if !shouldSucceed {
		assert.Assert(t, resp.StatusCode != http.StatusOK)
		return
	}
	assert.Equal(t, resp.StatusCode, http.StatusNoContent)
}
