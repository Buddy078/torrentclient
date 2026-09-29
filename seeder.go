package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
)

type seeder struct {
	infoHash [20]byte
	peerID   [20]byte
	bitfield bitfield
	pieceLen int
	storage  *storage_manager
	mu       *sync.RWMutex
}

func startSeeder(port int, tf torrent_file, peerID [20]byte, bf bitfield, sm *storage_manager, mu *sync.RWMutex) {
	s := &seeder{
		infoHash: tf.info_hash,
		peerID:   peerID,
		bitfield: bf,
		pieceLen: tf.piece_length,
		storage:  sm,
		mu:       mu,
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go s.handlePeer(conn)
	}
}

func (s *seeder) handlePeer(conn net.Conn) {
	defer conn.Close()

	hsBuf := make([]byte, 68)
	if _, err := io.ReadFull(conn, hsBuf); err != nil {
		return
	}

	if hsBuf[0] != 19 || string(hsBuf[1:20]) != "BitTorrent protocol" {
		return
	}

	var peerInfoHash [20]byte
	copy(peerInfoHash[:], hsBuf[28:48])
	if !bytes.Equal(peerInfoHash[:], s.infoHash[:]) {
		return
	}

	respHandshake := make([]byte, 68)
	respHandshake[0] = 19
	copy(respHandshake[1:20], "BitTorrent protocol")
	copy(respHandshake[28:48], s.infoHash[:])
	copy(respHandshake[48:68], s.peerID[:])

	if _, err := conn.Write(respHandshake); err != nil {
		return
	}

	s.mu.RLock()
	bfCopy := make([]byte, len(s.bitfield))
	copy(bfCopy, s.bitfield)
	s.mu.RUnlock()

	bfMsg := message{
		id:      msg_bitfield,
		payload: bfCopy,
	}
	if _, err := conn.Write(bfMsg.serialize()); err != nil {
		return
	}

	unchokeMsg := message{id: msg_unchoke}
	if _, err := conn.Write(unchokeMsg.serialize()); err != nil {
		return
	}

	for {
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return
		}

		length := binary.BigEndian.Uint32(lenBuf)
		if length == 0 {
			continue
		}

		msgBuf := make([]byte, length)
		if _, err := io.ReadFull(conn, msgBuf); err != nil {
			return
		}

		msgID := message_id(msgBuf[0])
		payload := msgBuf[1:]

		switch msgID {
		case msg_request:
			if len(payload) < 12 {
				continue
			}
			index := int(binary.BigEndian.Uint32(payload[0:4]))
			begin := int(binary.BigEndian.Uint32(payload[4:8]))
			reqLen := int(binary.BigEndian.Uint32(payload[8:12]))

			if reqLen > 16384 || reqLen <= 0 {
				continue
			}

			s.mu.RLock()
			hasPiece := s.bitfield.has_piece(index)
			s.mu.RUnlock()

			if !hasPiece {
				continue
			}

			fileOffset := int64(index*s.pieceLen + begin)
			blockData := make([]byte, reqLen)

			_, err := s.storage.ReadAt(blockData, fileOffset)
			if err != nil && err != io.EOF {
				continue
			}

			piecePayload := make([]byte, 8+reqLen)
			binary.BigEndian.PutUint32(piecePayload[0:4], uint32(index))
			binary.BigEndian.PutUint32(piecePayload[4:8], uint32(begin))
			copy(piecePayload[8:], blockData)

			respMsg := message{
				id:      msg_piece,
				payload: piecePayload,
			}
			if _, err := conn.Write(respMsg.serialize()); err != nil {
				return
			}

		case msg_interested:
			continue
		}
	}
}