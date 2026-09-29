package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"net/url"
	"time"
)

const (
	udpProtocolID       uint64 = 0x41727101980
	actionConnect       uint32 = 0
	actionAnnounce      uint32 = 1
	actionError         uint32 = 3
	udpTrackerTimeout          = 10 * time.Second
)

func request_peers_udp(u *url.URL, infoHash [20]byte, peerID [20]byte, port uint16, left int) ([]peer, error) {
	conn, err := net.DialTimeout("udp", u.Host, udpTrackerTimeout)
	if err != nil {
		return nil, fmt.Errorf("udp dial failed: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(udpTrackerTimeout))

	var transactionID uint32
	var b [4]byte
	_, _ = rand.Read(b[:])
	transactionID = binary.BigEndian.Uint32(b[:])

	reqBuf := new(bytes.Buffer)
	_ = binary.Write(reqBuf, binary.BigEndian, udpProtocolID)
	_ = binary.Write(reqBuf, binary.BigEndian, actionConnect)
	_ = binary.Write(reqBuf, binary.BigEndian, transactionID)

	if _, err := conn.Write(reqBuf.Bytes()); err != nil {
		return nil, err
	}

	respBuf := make([]byte, 16)
	n, err := conn.Read(respBuf)
	if err != nil || n < 16 {
		return nil, fmt.Errorf("failed to read connect response: %v", err)
	}

	resAction := binary.BigEndian.Uint32(respBuf[0:4])
	resTxID := binary.BigEndian.Uint32(respBuf[4:8])
	connectionID := binary.BigEndian.Uint64(respBuf[8:16])

	if resAction != actionConnect || resTxID != transactionID {
		return nil, fmt.Errorf("invalid connect response header")
	}

	_, _ = rand.Read(b[:])
	transactionID = binary.BigEndian.Uint32(b[:])

	annBuf := new(bytes.Buffer)
	_ = binary.Write(annBuf, binary.BigEndian, connectionID)
	_ = binary.Write(annBuf, binary.BigEndian, actionAnnounce)
	_ = binary.Write(annBuf, binary.BigEndian, transactionID)
	annBuf.Write(infoHash[:])
	annBuf.Write(peerID[:])
	_ = binary.Write(annBuf, binary.BigEndian, uint64(0))
	_ = binary.Write(annBuf, binary.BigEndian, uint64(left))
	_ = binary.Write(annBuf, binary.BigEndian, uint64(0))
	_ = binary.Write(annBuf, binary.BigEndian, uint32(0))
	_ = binary.Write(annBuf, binary.BigEndian, uint32(0))
	_ = binary.Write(annBuf, binary.BigEndian, uint32(0))
	_ = binary.Write(annBuf, binary.BigEndian, int32(100))
	_ = binary.Write(annBuf, binary.BigEndian, port)

	if _, err := conn.Write(annBuf.Bytes()); err != nil {
		return nil, err
	}

	annResp := make([]byte, 2048)
	n, err = conn.Read(annResp)
	if err != nil || n < 20 {
		return nil, fmt.Errorf("failed to read announce response: %v", err)
	}

	resAction = binary.BigEndian.Uint32(annResp[0:4])
	resTxID = binary.BigEndian.Uint32(annResp[4:8])

	if resAction == actionError {
		return nil, fmt.Errorf("udp tracker error: %s", string(annResp[8:n]))
	}

	if resAction != actionAnnounce || resTxID != transactionID {
		return nil, fmt.Errorf("invalid announce response action or transaction id")
	}

	return unmarshal_peers(annResp[20:n])
}