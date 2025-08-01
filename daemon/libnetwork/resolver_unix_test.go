//go:build !windows

package libnetwork

import (
	"context"
	"net"
	"testing"

	"github.com/miekg/dns"
	"github.com/moby/moby/v2/daemon/libnetwork/config"
	"github.com/moby/moby/v2/daemon/libnetwork/ipamutils"
	"github.com/moby/moby/v2/internal/testutils/netnsutils"
)

// test only works on linux
func TestDNSIPQuery(t *testing.T) {
	defer netnsutils.SetupTestOSContext(t)()
	c, err := New(context.Background(), config.OptionDataDir(t.TempDir()),
		config.OptionDefaultAddressPoolConfig(ipamutils.GetLocalScopeDefaultNetworks()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	n, err := c.NewNetwork(context.Background(), "bridge", "dtnet1", "", NetworkOptionEnableIPv4(true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep, err := n.CreateEndpoint(context.Background(), "testep")
	if err != nil {
		t.Fatal(err)
	}

	sb, err := c.NewSandbox(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := sb.Delete(context.Background()); err != nil {
			t.Fatal(err)
		}
	}()

	// we need the endpoint only to populate ep_list for the sandbox as part of resolve_name
	// it is not set as a target for name resolution and does not serve any other purpose
	err = ep.Join(context.Background(), sb)
	if err != nil {
		t.Fatal(err)
	}

	// add service records which are used to resolve names. These are the real targets for the DNS queries
	n.addSvcRecords("ep1", "name1", "svc1", net.ParseIP("192.168.0.1"), net.IP{}, true, "test")

	w := new(tstwriter)
	// the unit tests right now will focus on non-proxied DNS requests
	r := NewResolver(resolverIPSandbox, false, sb)

	// test name1's IP is resolved correctly with the default A type query
	// Also make sure DNS lookups are case insensitive
	names := []string{"name1.", "NaMe1."}
	for _, name := range names {
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeA)
		r.serveDNS(w, q)
		resp := w.GetResponse()
		checkNonNullResponse(t, resp)
		t.Log("Response: ", resp.String())
		checkDNSResponseCode(t, resp, dns.RcodeSuccess)
		checkDNSAnswersCount(t, resp, 1)
		checkDNSRRType(t, resp.Answer[0].Header().Rrtype, dns.TypeA)
		if answer, ok := resp.Answer[0].(*dns.A); ok {
			if !answer.A.Equal(net.ParseIP("192.168.0.1")) {
				t.Fatalf("IP response in Answer %v does not match 192.168.0.1", answer.A)
			}
		} else {
			t.Fatal("Answer of type A not found")
		}
		w.ClearResponse()
	}

	// test MX query with name1 results in Success response with 0 answer records
	q := new(dns.Msg)
	q.SetQuestion("name1.", dns.TypeMX)
	r.serveDNS(w, q)
	resp := w.GetResponse()
	checkNonNullResponse(t, resp)
	t.Log("Response: ", resp.String())
	checkDNSResponseCode(t, resp, dns.RcodeSuccess)
	checkDNSAnswersCount(t, resp, 0)
	w.ClearResponse()

	// test MX query with non existent name results in ServFail response with 0 answer records
	// since this is a unit test env, we disable proxying DNS above which results in ServFail rather than NXDOMAIN
	q = new(dns.Msg)
	q.SetQuestion("nonexistent.", dns.TypeMX)
	r.serveDNS(w, q)
	resp = w.GetResponse()
	checkNonNullResponse(t, resp)
	t.Log("Response: ", resp.String())
	checkDNSResponseCode(t, resp, dns.RcodeServerFailure)
	w.ClearResponse()
}

// test only works on linux
func TestDNSProxyServFail(t *testing.T) {
	osctx := netnsutils.SetupTestOSContextEx(t)
	defer osctx.Cleanup(t)

	c, err := New(context.Background(),
		config.OptionDataDir(t.TempDir()),
		config.OptionDefaultAddressPoolConfig(ipamutils.GetLocalScopeDefaultNetworks()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	n, err := c.NewNetwork(context.Background(), "bridge", "dtnet2", "", NetworkOptionEnableIPv4(true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	sb, err := c.NewSandbox(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := sb.Delete(context.Background()); err != nil {
			t.Fatal(err)
		}
	}()

	var nRequests int
	// initialize a local DNS server and configure it to fail the first query
	dns.HandleFunc(".", newDNSHandlerServFailOnce(&nRequests))
	// use TCP for predictable results. Connection tests (to figure out DNS server initialization) don't work with UDP
	server := &dns.Server{Addr: "127.0.0.1:53", Net: "tcp"}
	srvErrCh := make(chan error, 1)
	osctx.Go(t, func() {
		srvErrCh <- server.ListenAndServe()
	})
	defer func() {
		server.Shutdown() //nolint:errcheck
		if err := <-srvErrCh; err != nil {
			t.Error(err)
		}
	}()

	waitForLocalDNSServer(t)
	t.Log("DNS Server can be reached")

	w := new(tstwriter)
	r := NewResolver(resolverIPSandbox, true, sb)
	q := new(dns.Msg)
	q.SetQuestion("name1.", dns.TypeA)

	var localDNSEntries []extDNSEntry
	extTestDNSEntry := extDNSEntry{IPStr: "127.0.0.1", HostLoopback: true}

	// configure two external DNS entries and point both to local DNS server thread
	localDNSEntries = append(localDNSEntries, extTestDNSEntry)
	localDNSEntries = append(localDNSEntries, extTestDNSEntry)

	// this should generate two requests: the first will fail leading to a retry
	r.SetExtServers(localDNSEntries)
	r.serveDNS(w, q)
	if nRequests != 2 {
		t.Fatalf("Expected 2 DNS queries. Found: %d", nRequests)
	}
	t.Logf("Expected number of DNS requests generated")
}
