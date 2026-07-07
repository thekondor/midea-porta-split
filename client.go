// Copyright (c) 2026 Andrew 'kondor' Sichevoi. All rights reserved.
// Use of this source code is governed by an MIT license found in the LICENSE file.

package midea

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// ErrAuthFailed is returned when the device rejects the token.
var ErrAuthFailed = errors.New("midea: authentication failed (device returned ERROR)")

// Options configures the Client.
type Options struct {
	DialTimeout       time.Duration
	ReadTimeout       time.Duration
	HeartbeatInterval time.Duration
	PostAuthDelay     time.Duration
	CmdAttemptTimeout time.Duration // per-attempt read wait before resending
	CmdMaxAttempts    int           // max send attempts per command (0 = use default)
}

// DefaultOptions returns the recommended Options for most use cases.
func DefaultOptions() Options {
	return Options{
		DialTimeout:       10 * time.Second,
		ReadTimeout:       10 * time.Second,
		HeartbeatInterval: 10 * time.Second,
		PostAuthDelay:     1 * time.Second,
		CmdAttemptTimeout: 2 * time.Second,
		CmdMaxAttempts:    10,
	}
}

// Client manages a persistent TCP connection to a Midea V3 LAN AC unit.
// A background goroutine sends 8370 heartbeats every HeartbeatInterval to keep
// the connection alive. The connection is re-established automatically on the
// next Poll or Update call after a failure.
//
// Client methods are safe for concurrent use.
type Client struct {
	addr     string // host:6444
	token    []byte // 64 bytes
	key      []byte // 32 bytes
	deviceID uint64

	mu       sync.Mutex
	conn     net.Conn
	tcpKey   []byte
	reqCount uint16
	msgID    uint8
	readBuf  []byte
	lastResp *Response

	stopCh chan struct{}
	opts   Options
	dbg    io.Writer
}

// NewClient creates a Client for the device at addr with the given token (64
// bytes) and key (32 bytes). deviceID is embedded in 5A5A packets; pass 0 if
// unknown.
//
// addr may be "host" (port 6444 is assumed) or "host:port".
func NewClient(addr string, token, key []byte, deviceID uint64, opts Options) (*Client, error) {
	if len(token) != 64 {
		return nil, fmt.Errorf("midea: token must be 64 bytes, got %d", len(token))
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("midea: key must be 32 bytes, got %d", len(key))
	}
	if !strings.Contains(addr, ":") {
		addr = addr + ":6444"
	}
	if opts.DialTimeout == 0 {
		opts = DefaultOptions()
	}
	return &Client{
		addr:     addr,
		token:    token,
		key:      key,
		deviceID: deviceID,
		opts:     opts,
		msgID:    1,
	}, nil
}

// SetDebugWriter enables debug logging of raw bytes to w (nil = silent).
func (c *Client) SetDebugWriter(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dbg = w
}

// Connect opens a TCP connection to the device and performs the V3 handshake.
// It also launches the background heartbeat goroutine. Call Close to shut down.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectLocked(ctx)
}

// connectLocked performs the full connect+handshake. mu must be held.
func (c *Client) connectLocked(ctx context.Context) error {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.tcpKey = nil
		c.readBuf = nil
	}

	dialer := &net.Dialer{Timeout: c.opts.DialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return fmt.Errorf("midea: dial %s: %w", c.addr, err)
	}
	c.conn = conn
	c.reqCount = 0
	c.readBuf = nil

	// Step 1: send 8370 handshake request with the full 64-byte token.
	handshake := encode8370(c.token, msgtypeHandshakeRequest, nil, &c.reqCount)
	c.debugf("→ HANDSHAKE %d bytes\n", len(handshake))
	if err := c.writeConn(handshake); err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return fmt.Errorf("midea: send handshake: %w", err)
	}

	// Step 2: read handshake response.
	resp, err := c.readHandshakeResponse()
	if err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return fmt.Errorf("midea: read handshake response: %w", err)
	}
	c.debugf("← HANDSHAKE RESPONSE %d bytes\n", len(resp))

	// Step 3: derive tcp_key.
	tcpKey, err := deriveTCPKey(resp, c.key)
	if err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return fmt.Errorf("midea: derive tcp_key: %w", err)
	}
	c.tcpKey = tcpKey

	// Step 4: 1-second post-auth delay.
	select {
	case <-ctx.Done():
		_ = c.conn.Close()
		c.conn = nil
		return ctx.Err()
	case <-time.After(c.opts.PostAuthDelay):
	}

	// Step 5: start heartbeat goroutine.
	if c.stopCh != nil {
		close(c.stopCh)
	}
	c.stopCh = make(chan struct{})
	go c.heartbeatLoop(c.stopCh)

	return nil
}

// readHandshakeResponse reads the raw device response to the handshake request
// and returns the 64-byte payload at bytes [8:72].
//
// The Python reference implementation does not decode the 8370 framing here —
// it just reads the raw TCP bytes and slices [8:72] directly:
//
//	response = self._socket.recv(512)
//	response = response[8:72]
func (c *Client) readHandshakeResponse() ([]byte, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(c.opts.ReadTimeout)); err != nil {
		return nil, err
	}
	var buf []byte
	tmp := make([]byte, 512)
	for {
		n, err := c.conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return nil, err
		}
		if len(buf) >= 5 && bytes.Equal(buf[:5], []byte("ERROR")) {
			return nil, ErrAuthFailed
		}
		if len(buf) >= 72 {
			return buf[8:72], nil
		}
	}
}

// Close shuts down the heartbeat goroutine and closes the TCP connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopCh != nil {
		close(c.stopCh)
		c.stopCh = nil
	}
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		c.tcpKey = nil
		c.readBuf = nil
		return err
	}
	return nil
}

// Poll reads the current device state.
func (c *Client) Poll(ctx context.Context) (Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureConnectedLocked(ctx); err != nil {
		return Response{}, err
	}

	resp, err := c.sendAndReadC0Locked(ctx, func() []byte {
		frame := buildQueryCmd(&c.msgID)
		pkt := build5A5A(c.deviceID, frame)
		msg := encode8370(pkt, msgtypeEncryptedRequest, c.tcpKey, &c.reqCount)
		c.debugf("→ QUERY %d bytes\n", len(msg))
		return msg
	})
	if err != nil {
		return Response{}, fmt.Errorf("midea: poll: %w", err)
	}
	c.lastResp = &resp
	return resp, nil
}

// Update applies the non-nil fields of req to the device and returns the
// resulting state. If no prior Poll has been done, one is performed first to
// seed the full state needed for the Midea set command.
//
// Display toggle is special: it uses a separate QUERY-type command (not the
// 0x40 set command). If req.Display is set and differs from the current state,
// that command is sent first; remaining non-Display fields are applied via the
// normal 0x40 set command afterward.
func (c *Client) Update(ctx context.Context, req Request) (Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureConnectedLocked(ctx); err != nil {
		return Response{}, err
	}

	// Seed lastResp if needed (Midea requires full state on every set).
	if c.lastResp == nil {
		c.mu.Unlock()
		resp, err := c.Poll(ctx)
		c.mu.Lock()
		if err != nil {
			return Response{}, fmt.Errorf("midea: update: seed poll: %w", err)
		}
		c.lastResp = &resp
	}

	// Display toggle must use its own QUERY-type command.
	if req.Display != nil && *req.Display != c.lastResp.DisplayOn {
		beep := req.Beep == nil || *req.Beep
		resp, err := c.sendAndReadC0Locked(ctx, func() []byte {
			frame := buildToggleDisplayCmd(beep, &c.msgID)
			pkt := build5A5A(c.deviceID, frame)
			msg := encode8370(pkt, msgtypeEncryptedRequest, c.tcpKey, &c.reqCount)
			c.debugf("→ TOGGLE DISPLAY %d bytes\n", len(msg))
			return msg
		})
		if err != nil {
			return Response{}, fmt.Errorf("midea: update: toggle display: %w", err)
		}
		c.lastResp = &resp
	}

	// Apply remaining non-Display fields via the 0x40 set command.
	setReq := req
	setReq.Display = nil
	if !isEmptyRequest(setReq) {
		state := applyRequest(*c.lastResp, setReq)
		resp, err := c.sendAndReadC0Locked(ctx, func() []byte {
			frame := buildSetCmd(state, &c.msgID)
			pkt := build5A5A(c.deviceID, frame)
			msg := encode8370(pkt, msgtypeEncryptedRequest, c.tcpKey, &c.reqCount)
			c.debugf("→ SET %d bytes\n", len(msg))
			return msg
		})
		if err != nil {
			return Response{}, fmt.Errorf("midea: update: %w", err)
		}
		c.lastResp = &resp
	}

	return *c.lastResp, nil
}

// isEmptyRequest reports whether all fields of r are nil (no changes requested).
func isEmptyRequest(r Request) bool {
	return r.Power == nil && r.Mode == nil && r.TargetTemp == nil &&
		r.FanSpeed == nil && r.SwingV == nil && r.SwingH == nil &&
		r.Eco == nil && r.Turbo == nil && r.Sleep == nil &&
		r.Fahrenheit == nil && r.FrostProtect == nil &&
		r.ComfortMode == nil && r.Beep == nil && r.FollowMe == nil &&
		r.Purifier == nil && r.AuxHeat == nil && r.TargetHumidity == nil
}

// sendAndReadC0Locked sends a command (built by buildMsg on each attempt) and
// reads until a 0xC0 response arrives. If no response comes within
// CmdAttemptTimeout the command is resent, up to CmdMaxAttempts times total.
// Hard I/O errors and context cancellation abort immediately.
// mu must be held.
func (c *Client) sendAndReadC0Locked(ctx context.Context, buildMsg func() []byte) (Response, error) {
	maxAttempts := c.opts.CmdMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	attemptTimeout := c.opts.CmdAttemptTimeout
	if attemptTimeout <= 0 {
		attemptTimeout = 2 * time.Second
	}

	var ctxDeadline time.Time
	if d, ok := ctx.Deadline(); ok {
		ctxDeadline = d
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}

		msg := buildMsg()
		if err := c.writeConn(msg); err != nil {
			c.markDeadLocked()
			return Response{}, fmt.Errorf("write (attempt %d): %w", attempt+1, err)
		}

		// Per-attempt deadline: whichever is sooner — context deadline or attempt timeout.
		attemptDeadline := time.Now().Add(attemptTimeout)
		if !ctxDeadline.IsZero() && ctxDeadline.Before(attemptDeadline) {
			attemptDeadline = ctxDeadline
		}

		resp, err := c.readUntilC0Locked(attemptDeadline)
		if err == nil {
			return resp, nil
		}

		// Distinguish timeout (retry) from hard error (abort).
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			c.debugf("← attempt %d: timeout, retrying\n", attempt+1)
			continue
		}
		c.markDeadLocked()
		return Response{}, err
	}

	c.markDeadLocked()
	return Response{}, fmt.Errorf("no response after %d attempts", maxAttempts)
}

// ensureConnectedLocked connects if not already connected. mu must be held.
func (c *Client) ensureConnectedLocked(ctx context.Context) error {
	if c.conn == nil || c.tcpKey == nil {
		return c.connectLocked(ctx)
	}
	return nil
}

// markDeadLocked closes the connection so the next call triggers a reconnect.
func (c *Client) markDeadLocked() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.tcpKey = nil
		c.readBuf = nil
	}
}

// heartbeatLoop sends a 5A5A heartbeat every HeartbeatInterval.
// Runs in its own goroutine; exits when stopCh is closed.
func (c *Client) heartbeatLoop(stopCh <-chan struct{}) {
	ticker := time.NewTicker(c.opts.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			c.mu.Lock()
			if c.conn != nil && c.tcpKey != nil {
				hb := encode8370(heartbeat5A5A(c.deviceID), msgtypeEncryptedRequest, c.tcpKey, &c.reqCount)
				c.debugf("→ HEARTBEAT %d bytes\n", len(hb))
				if err := c.writeConn(hb); err != nil {
					c.debugf("heartbeat write error: %v\n", err)
					c.markDeadLocked()
				}
			}
			c.mu.Unlock()
		}
	}
}

// readUntilBodyLocked reads 8370 frames until one with a matching body type is
// found. Frames with non-matching body types (e.g. heartbeat ACKs) are
// silently discarded. mu must be held.
func (c *Client) readUntilBodyLocked(deadline time.Time, want ...byte) (bodyType byte, body []byte, _ error) {
	for {
		pkts, leftover, err := decode8370(c.readBuf, c.tcpKey)
		c.readBuf = leftover
		if err != nil {
			return 0, nil, err
		}

		for _, pkt := range pkts {
			cmdFrame, extractErr := extractCommandFrame(pkt)
			if extractErr != nil {
				c.debugf("extract frame error: %v\n", extractErr)
				continue
			}
			if cmdFrame == nil {
				continue // heartbeat
			}
			bt, b, parseErr := parseCommandFrame(cmdFrame)
			if parseErr != nil {
				c.debugf("parse frame error: %v\n", parseErr)
				continue
			}
			if b == nil {
				continue
			}
			for _, w := range want {
				if bt == w {
					c.debugf("← 0x%02X response\n", bt)
					return bt, b, nil
				}
			}
		}

		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return 0, nil, err
		}
		tmp := make([]byte, 4096)
		n, readErr := c.conn.Read(tmp)
		if n > 0 {
			c.readBuf = append(c.readBuf, tmp[:n]...)
		}
		if readErr != nil {
			return 0, nil, fmt.Errorf("midea: read: %w", readErr)
		}
	}
}

// readUntilC0Locked is a convenience wrapper around readUntilBodyLocked for
// the common case of waiting for a 0xC0 status response.
func (c *Client) readUntilC0Locked(deadline time.Time) (Response, error) {
	_, body, err := c.readUntilBodyLocked(deadline, 0xC0)
	if err != nil {
		return Response{}, err
	}
	return parseC0Body(body)
}

// sendAndReadBodyLocked is a generalised version of sendAndReadC0Locked that
// waits for any of the specified body types. mu must be held.
func (c *Client) sendAndReadBodyLocked(ctx context.Context, buildMsg func() []byte, want ...byte) (byte, []byte, error) {
	maxAttempts := c.opts.CmdMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	attemptTimeout := c.opts.CmdAttemptTimeout
	if attemptTimeout <= 0 {
		attemptTimeout = 2 * time.Second
	}

	var ctxDeadline time.Time
	if d, ok := ctx.Deadline(); ok {
		ctxDeadline = d
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		msg := buildMsg()
		if err := c.writeConn(msg); err != nil {
			c.markDeadLocked()
			return 0, nil, fmt.Errorf("write (attempt %d): %w", attempt+1, err)
		}
		attemptDeadline := time.Now().Add(attemptTimeout)
		if !ctxDeadline.IsZero() && ctxDeadline.Before(attemptDeadline) {
			attemptDeadline = ctxDeadline
		}
		bt, body, err := c.readUntilBodyLocked(attemptDeadline, want...)
		if err == nil {
			return bt, body, nil
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			c.debugf("← attempt %d: timeout, retrying\n", attempt+1)
			continue
		}
		c.markDeadLocked()
		return 0, nil, err
	}
	c.markDeadLocked()
	return 0, nil, fmt.Errorf("no response after %d attempts", maxAttempts)
}

// Capabilities queries device capabilities via two 0xB5 commands (standard +
// additional) and merges the results.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureConnectedLocked(ctx); err != nil {
		return Capabilities{}, err
	}

	merged := Capabilities{Raw: make(map[uint16][]byte)}

	for _, additional := range []bool{false, true} {
		add := additional // capture
		_, body, err := c.sendAndReadBodyLocked(ctx, func() []byte {
			frame := buildCapabilitiesCmd(add, &c.msgID)
			pkt := build5A5A(c.deviceID, frame)
			return encode8370(pkt, msgtypeEncryptedRequest, c.tcpKey, &c.reqCount)
		}, 0xB5)
		if err != nil {
			return Capabilities{}, fmt.Errorf("midea: capabilities: %w", err)
		}
		caps, err := parseCapabilitiesBody(body)
		if err != nil {
			return Capabilities{}, fmt.Errorf("midea: capabilities: %w", err)
		}
		// Merge: raw map is additive; bool flags are OR'd.
		for id, v := range caps.Raw {
			merged.Raw[id] = v
		}
		merged.CustomFanSpeed = merged.CustomFanSpeed || caps.CustomFanSpeed
		merged.HumidityControl = merged.HumidityControl || caps.HumidityControl
		merged.SwingAngle = merged.SwingAngle || caps.SwingAngle
		merged.FreshAir = merged.FreshAir || caps.FreshAir
		merged.Breeze = merged.Breeze || caps.Breeze
		merged.Purifier = merged.Purifier || caps.Purifier
		merged.OutSilent = merged.OutSilent || caps.OutSilent
		if caps.MinCoolTemp != 0 {
			merged.MinCoolTemp = caps.MinCoolTemp
		}
		if caps.MaxCoolTemp != 0 {
			merged.MaxCoolTemp = caps.MaxCoolTemp
		}
		if caps.MinHeatTemp != 0 {
			merged.MinHeatTemp = caps.MinHeatTemp
		}
		if caps.MaxHeatTemp != 0 {
			merged.MaxHeatTemp = caps.MaxHeatTemp
		}
	}
	return merged, nil
}

// Energy queries power consumption statistics.
func (c *Client) Energy(ctx context.Context) (Energy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureConnectedLocked(ctx); err != nil {
		return Energy{}, err
	}

	_, body, err := c.sendAndReadBodyLocked(ctx, func() []byte {
		frame := buildEnergyCmd(&c.msgID)
		pkt := build5A5A(c.deviceID, frame)
		return encode8370(pkt, msgtypeEncryptedRequest, c.tcpKey, &c.reqCount)
	}, 0xC1)
	if err != nil {
		return Energy{}, fmt.Errorf("midea: energy: %w", err)
	}
	e, err := parseEnergyBody(body)
	if err != nil {
		return Energy{}, fmt.Errorf("midea: energy: %w", err)
	}
	return e, nil
}

// SetProperties applies advanced properties via the 0xB0 command. Properties
// with nil values are skipped. Returns nil on success; the device may or may
// not send a full state update in reply — this method does not update lastResp.
func (c *Client) SetProperties(ctx context.Context, props PropertiesRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureConnectedLocked(ctx); err != nil {
		return err
	}

	// Accept either a 0xB1 properties-ack or a 0xC0 state update.
	_, _, err := c.sendAndReadBodyLocked(ctx, func() []byte {
		frame := buildSetPropertiesCmd(props, &c.msgID)
		pkt := build5A5A(c.deviceID, frame)
		return encode8370(pkt, msgtypeEncryptedRequest, c.tcpKey, &c.reqCount)
	}, 0xB1, 0xC0)
	if err != nil {
		return fmt.Errorf("midea: set properties: %w", err)
	}
	return nil
}

func (c *Client) writeConn(data []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.opts.ReadTimeout)); err != nil {
		return err
	}
	_, err := c.conn.Write(data)
	return err
}

func (c *Client) debugf(format string, args ...any) {
	if c.dbg != nil {
		_, _ = fmt.Fprintf(c.dbg, format, args...)
	}
}

