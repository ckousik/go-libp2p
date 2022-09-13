package libp2pwebrtc

// udpMuxNewAddr is mostly similar to UDPMuxDefault exported from
// pion/ice [https://github.com/pion/ice/blob/master/udp_mux.go] with
// the only difference being the additional channel to notify the libp2p
// transport of any STUN requests from unknown ufrags.

import (
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/pion/ice/v2"
	"github.com/pion/logging"
	"github.com/pion/stun"
)

type candidateAddr struct {
	raddr *net.UDPAddr
	ufrag string
}

var _ ice.UDPMux = &udpMuxNewAddr{}

// udpMuxNewAddr is an implementation of the interface
type udpMuxNewAddr struct {
	params ice.UDPMuxParams

	closedChan chan struct{}
	closeOnce  sync.Once

	// connsIPv4 and connsIPv6 are maps of all udpMuxedConn indexed by ufrag|network|candidateType
	connsIPv4, connsIPv6 map[string]*udpMuxedConn

	addressMapMu sync.RWMutex
	addressMap   map[string]*udpMuxedConn

	// buffer pool to recycle buffers for net.UDPAddr encodes/decodes
	pool *sync.Pool

	mu sync.Mutex

	newAddrChan chan candidateAddr
	newAddrs    map[*net.UDPAddr]struct{}
}

const maxAddrSize = 512
const receiveMTU = 1500

// NewUDPMuxNewAddr creates an implementation of UDPMux
func NewUDPMuxNewAddr(params ice.UDPMuxParams, newAddrChan chan candidateAddr) *udpMuxNewAddr {
	if params.Logger == nil {
		params.Logger = logging.NewDefaultLoggerFactory().NewLogger("ice")
	}

	m := &udpMuxNewAddr{
		addressMap: map[string]*udpMuxedConn{},
		params:     params,
		connsIPv4:  make(map[string]*udpMuxedConn),
		connsIPv6:  make(map[string]*udpMuxedConn),
		closedChan: make(chan struct{}),
		pool: &sync.Pool{
			New: func() interface{} {
				// big enough buffer to fit both packet and address
				return newBufferHolder(receiveMTU + maxAddrSize)
			},
		},
		newAddrChan: newAddrChan,
		newAddrs:    make(map[*net.UDPAddr]struct{}),
	}

	go m.connWorker()

	return m
}

// LocalAddr returns the listening address of this UDPMuxNewAddr
func (m *udpMuxNewAddr) LocalAddr() net.Addr {
	return m.params.UDPConn.LocalAddr()
}

// GetConn returns a PacketConn given the connection's ufrag and network
// creates the connection if an existing one can't be found
func (m *udpMuxNewAddr) GetConn(ufrag string, isIPv6 bool) (net.PacketConn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.IsClosed() {
		return nil, io.ErrClosedPipe
	}

	if conn, ok := m.getConn(ufrag, isIPv6); ok {
		return conn, nil
	}

	c := m.createMuxedConn(ufrag)
	go func() {
		<-c.CloseChannel()
		m.removeConn(ufrag)
	}()

	if isIPv6 {
		m.connsIPv6[ufrag] = c
	} else {
		m.connsIPv4[ufrag] = c
	}

	return c, nil
}

// RemoveConnByUfrag stops and removes the muxed packet connection
func (m *udpMuxNewAddr) RemoveConnByUfrag(ufrag string) {
	removedConns := make([]*udpMuxedConn, 0, 2)

	// Keep lock section small to avoid deadlock with conn lock
	m.mu.Lock()
	if c, ok := m.connsIPv4[ufrag]; ok {
		delete(m.connsIPv4, ufrag)
		removedConns = append(removedConns, c)
	}
	if c, ok := m.connsIPv6[ufrag]; ok {
		delete(m.connsIPv6, ufrag)
		removedConns = append(removedConns, c)
	}
	m.mu.Unlock()

	m.addressMapMu.Lock()
	defer m.addressMapMu.Unlock()

	for _, c := range removedConns {
		addresses := c.getAddresses()
		for _, addr := range addresses {
			delete(m.addressMap, addr)
		}
	}
}

// IsClosed returns true if the mux had been closed
func (m *udpMuxNewAddr) IsClosed() bool {
	select {
	case <-m.closedChan:
		return true
	default:
		return false
	}
}

// Close the mux, no further connections could be created
func (m *udpMuxNewAddr) Close() error {
	var err error
	m.closeOnce.Do(func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		for _, c := range m.connsIPv4 {
			_ = c.Close()
		}
		for _, c := range m.connsIPv6 {
			_ = c.Close()
		}

		m.connsIPv4 = make(map[string]*udpMuxedConn)
		m.connsIPv6 = make(map[string]*udpMuxedConn)

		close(m.closedChan)
	})
	return err
}

func (m *udpMuxNewAddr) removeConn(key string) {
	// keep lock section small to avoid deadlock with conn lock
	c := func() *udpMuxedConn {
		m.mu.Lock()
		defer m.mu.Unlock()

		if c, ok := m.connsIPv4[key]; ok {
			delete(m.connsIPv4, key)
			return c
		}

		if c, ok := m.connsIPv6[key]; ok {
			delete(m.connsIPv6, key)
			return c
		}

		return nil
	}()

	if c == nil {
		return
	}

	m.addressMapMu.Lock()
	defer m.addressMapMu.Unlock()

	addresses := c.getAddresses()
	for _, addr := range addresses {
		delete(m.addressMap, addr)
	}
}

func (m *udpMuxNewAddr) writeTo(buf []byte, raddr net.Addr) (n int, err error) {
	return m.params.UDPConn.WriteTo(buf, raddr)
}

func (m *udpMuxNewAddr) registerConnForAddress(conn *udpMuxedConn, addr string) {
	if m.IsClosed() {
		return
	}

	m.addressMapMu.Lock()
	defer m.addressMapMu.Unlock()

	existing, ok := m.addressMap[addr]
	if ok {
		existing.removeAddress(addr)
	}
	m.addressMap[addr] = conn

	m.params.Logger.Debugf("Registered %s for %s", addr, conn.params.Key)
}

func (m *udpMuxNewAddr) createMuxedConn(key string) *udpMuxedConn {
	c := newUDPMuxedConn(&udpMuxedConnParams{
		Mux:       m,
		Key:       key,
		AddrPool:  m.pool,
		LocalAddr: m.LocalAddr(),
		Logger:    m.params.Logger,
	})
	return c
}

func (m *udpMuxNewAddr) connWorker() {
	logger := m.params.Logger

	defer func() {
		_ = m.Close()
	}()

	buf := make([]byte, receiveMTU)
	for {
		n, addr, err := m.params.UDPConn.ReadFrom(buf)
		if m.IsClosed() {
			return
		} else if err != nil {
			if os.IsTimeout(err) {
				continue
			} else if err != io.EOF {
				logger.Errorf("could not read udp packet: %v", err)
			}

			return
		}

		udpAddr, ok := addr.(*net.UDPAddr)
		if !ok {
			logger.Errorf("underlying PacketConn did not return a UDPAddr")
			return
		}

		// If we have already seen this address dispatch to the appropriate destination
		m.addressMapMu.Lock()
		destinationConn := m.addressMap[addr.String()]
		m.addressMapMu.Unlock()

		// If we haven't seen this address before but is a STUN packet lookup by ufrag
		if destinationConn == nil && stun.IsMessage(buf[:n]) {
			msg := &stun.Message{
				Raw: append([]byte{}, buf[:n]...),
			}
			// log.Info("received new stun message: %v", *msg)

			if err = msg.Decode(); err != nil {
				m.params.Logger.Warnf("Failed to handle decode ICE from %s: %v\n", addr.String(), err)
				continue
			}

			ufrag, ufragErr := ufragFromStunMessage(msg, false)
			if ufragErr != nil {
				m.params.Logger.Warnf("%v", ufragErr)
				log.Warnf("%v", ufragErr)
				continue
			}

			isIPv6 := udpAddr.IP.To4() == nil

			m.mu.Lock()
			destinationConn, ok = m.getConn(ufrag, isIPv6)
			m.mu.Unlock()

			// notify that a new connection is requested
			if !ok {
				// log.Debugf("new connection requested: %v %v", udpAddr, ufrag)
				m.newAddrChan <- candidateAddr{raddr: udpAddr, ufrag: ufrag}
				m.mu.Lock()
				m.newAddrs[udpAddr] = struct{}{}
				m.mu.Unlock()
				dc, _ := m.GetConn(ufrag, isIPv6)
				destinationConn = dc.(*udpMuxedConn)
			}
		}

		if destinationConn == nil {
			m.params.Logger.Debugf("dropping packet from %s, addr: %s", udpAddr.String(), addr.String())
			continue
		}

		if err = destinationConn.writePacket(buf[:n], udpAddr); err != nil {
			m.params.Logger.Errorf("could not write packet: %v", err)
		}
	}
}

func ufragFromStunMessage(msg *stun.Message, local_ufrag bool) (string, error) {
	attr, stunAttrErr := msg.Get(stun.AttrUsername)
	if stunAttrErr != nil {
		return "", stunAttrErr
	}
	ufrag := strings.Split(string(attr), ":")
	if len(ufrag) < 2 {
		return "", errors.New("invalid STUN username attribute")
	}
	if local_ufrag {
		return ufrag[1], nil
	} else {
		return ufrag[0], nil
	}
}

func (m *udpMuxNewAddr) getConn(ufrag string, isIPv6 bool) (val *udpMuxedConn, ok bool) {
	if isIPv6 {
		val, ok = m.connsIPv6[ufrag]
	} else {
		val, ok = m.connsIPv4[ufrag]
	}
	return
}

type bufferHolder struct {
	buffer []byte
}

func newBufferHolder(size int) *bufferHolder {
	return &bufferHolder{
		buffer: make([]byte, size),
	}
}
