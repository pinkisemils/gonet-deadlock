package thrash

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"testing"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

type netTun struct {
	ep             *channel.Endpoint
	stack          *stack.Stack
	incomingPacket chan *buffer.View
	mtu            int
	lock           sync.Mutex
}

func createNetTUN(localAddress netip.Addr) (*netTun, error) {
	opts := stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol6, icmp.NewProtocol4},
		HandleLocal:        true,
	}
	dev := &netTun{
		ep:             channel.New(1024, uint32(1280), ""),
		stack:          stack.New(opts),
		incomingPacket: make(chan *buffer.View),
		mtu:            1280,
	}
	dev.ep.AddNotify(dev)
	tcpipErr := dev.stack.CreateNIC(1, dev.ep)
	if tcpipErr != nil {
		return nil, fmt.Errorf("CreateNIC: %v", tcpipErr)
	}
	protoNumber := ipv4.ProtocolNumber

	protoAddr := tcpip.ProtocolAddress{
		Protocol:          protoNumber,
		AddressWithPrefix: tcpip.AddrFromSlice(localAddress.AsSlice()).WithPrefix(),
	}
	tcpipErr = dev.stack.AddProtocolAddress(1, protoAddr, stack.AddressProperties{})
	if tcpipErr != nil {
		return nil, fmt.Errorf("AddProtocolAddress(%v): %v", localAddress, tcpipErr)
	}

	dev.stack.AddRoute(tcpip.Route{Destination: header.IPv4EmptySubnet, NIC: 1})

	return dev, nil
}

func (net *netTun) DialContextTCP(addr netip.AddrPort) (*gonet.TCPConn, error) {
	fa, pn := convertToFullAddr(addr)
	return gonet.DialContextTCP(context.Background(), net.stack, fa, pn)
}

func convertToFullAddr(endpoint netip.AddrPort) (tcpip.FullAddress, tcpip.NetworkProtocolNumber) {
	var protoNumber tcpip.NetworkProtocolNumber
	if endpoint.Addr().Is4() {
		protoNumber = ipv4.ProtocolNumber
	} else {
		protoNumber = ipv6.ProtocolNumber
	}
	return tcpip.FullAddress{
		NIC:  1,
		Addr: tcpip.AddrFromSlice(endpoint.Addr().AsSlice()),
		Port: endpoint.Port(),
	}, protoNumber
}

func (tun *netTun) WriteNotify() {
	pkt := tun.ep.Read()
	if pkt == nil {
		return
	}

	view := pkt.ToView()
	pkt.DecRef()

	tun.incomingPacket <- view
}

func (tun *netTun) Close() error {
	tun.ep.Close()

	tun.stack.Close()
	tun.stack.RemoveNIC(1)


	if tun.incomingPacket != nil {
		close(tun.incomingPacket)
	}

	return nil
}

func TestThrash(t *testing.T) {
	localAddr := netip.MustParseAddr("10.1.1.1")
	remoteAddr := netip.MustParseAddrPort("10.2.2.2:111")

	tun, err := createNetTUN(localAddr)
	if err != nil {
		t.Fatalf("Failed to create tun: %v", err)
	}

	for i := 0; i < 1000; i += 1 {
		go tun.DialContextTCP(remoteAddr)
	}

	tun.Close()
}
