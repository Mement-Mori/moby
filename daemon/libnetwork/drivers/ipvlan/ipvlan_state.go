//go:build linux

package ipvlan

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/log"
	"github.com/moby/moby/v2/daemon/libnetwork/types"
)

func (d *driver) network(nid string) *network {
	d.Lock()
	n, ok := d.networks[nid]
	d.Unlock()
	if !ok {
		log.G(context.TODO()).Errorf("network id %s not found", nid)
	}

	return n
}

func (d *driver) addNetwork(n *network) {
	d.Lock()
	d.networks[n.id] = n
	d.Unlock()
}

func (d *driver) deleteNetwork(nid string) {
	d.Lock()
	delete(d.networks, nid)
	d.Unlock()
}

// getNetworks Safely returns a slice of existing networks
func (d *driver) getNetworks() []*network {
	d.Lock()
	defer d.Unlock()

	ls := make([]*network, 0, len(d.networks))
	for _, nw := range d.networks {
		ls = append(ls, nw)
	}

	return ls
}

func (n *network) endpoint(eid string) *endpoint {
	n.Lock()
	defer n.Unlock()

	return n.endpoints[eid]
}

func (n *network) addEndpoint(ep *endpoint) {
	n.Lock()
	n.endpoints[ep.id] = ep
	n.Unlock()
}

func (n *network) deleteEndpoint(eid string) {
	n.Lock()
	delete(n.endpoints, eid)
	n.Unlock()
}

func (n *network) getEndpoint(eid string) (*endpoint, error) {
	n.Lock()
	defer n.Unlock()
	if eid == "" {
		return nil, fmt.Errorf("endpoint id %s not found", eid)
	}
	if ep, ok := n.endpoints[eid]; ok {
		return ep, nil
	}

	return nil, nil
}

func validateID(nid, eid string) error {
	if nid == "" {
		return errors.New("invalid network id")
	}
	if eid == "" {
		return errors.New("invalid endpoint id")
	}

	return nil
}

func (d *driver) getNetwork(id string) (*network, error) {
	d.Lock()
	defer d.Unlock()
	if id == "" {
		return nil, types.InvalidParameterErrorf("invalid network id: %s", id)
	}

	if nw, ok := d.networks[id]; ok {
		return nw, nil
	}

	return nil, types.NotFoundErrorf("network not found: %s", id)
}
