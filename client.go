package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"time"
)

type bitfield []byte

func (bf bitfield) has_piece(index int) bool {
	byte_index := index / 8
	bit_offset := index % 8
	if byte_index >= len(bf) {
		return false
	}
	return bf[byte_index]&(1<<(7-bit_offset)) != 0
}

func (bf bitfield) set_piece(index int) {
	byte_index := index / 8
	bit_offset := index % 8
	if byte_index >= len(bf) {
		return
	}
	bf[byte_index] |= 1 << (7 - bit_offset)
}

type client struct {
	conn      net.Conn
	peer      peer
	info_hash [20]byte
	peer_id   [20]byte
	bitfield  bitfield
	choked    bool
}

func new_client(p peer, info_hash [20]byte, peer_id [20]byte, num_pieces int) (*client, error) {
	conn, err := net.DialTimeout("tcp", p.String(), 10*time.Second)
	if err != nil {
		return nil, err
	}

	if err := do_handshake(conn, info_hash, peer_id); err != nil {
		conn.Close()
		return nil, err
	}

	bf, initialMsg, err := recv_bitfield(conn, num_pieces)
	if err != nil {
		conn.Close()
		return nil, err
	}

	c := &client{
		conn:      conn,
		peer:      p,
		info_hash: info_hash,
		peer_id:   peer_id,
		bitfield:  bf,
		choked:    true,
	}

	if initialMsg != nil {
		switch initialMsg.id {
		case msg_unchoke:
			c.choked = false
		case msg_have:
			index, err := parse_have(initialMsg)
			if err == nil {
				c.bitfield.set_piece(index)
			}
		}
	}

	return c, nil
}

func do_handshake(conn net.Conn, info_hash [20]byte, peer_id [20]byte) error {
	req := make([]byte, 68)
	req[0] = 19
	copy(req[1:20], "BitTorrent protocol")
	copy(req[28:48], info_hash[:])
	copy(req[48:68], peer_id[:])

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetDeadline(time.Time{})

	if _, err := conn.Write(req); err != nil {
		return err
	}

	res := make([]byte, 68)
	if _, err := io.ReadFull(conn, res); err != nil {
		return err
	}

	if res[0] != 19 || string(res[1:20]) != "BitTorrent protocol" {
		return fmt.Errorf("invalid handshake from peer")
	}

	if !bytes.Equal(res[28:48], info_hash[:]) {
		return fmt.Errorf("peer info-hash does not match")
	}

	return nil
}

func recv_bitfield(conn net.Conn, num_pieces int) (bitfield, *message, error) {
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetDeadline(time.Time{})

	msg, err := read_message(conn)
	if err != nil {
		return nil, nil, err
	}
	if msg == nil {
		return make(bitfield, (num_pieces+7)/8), nil, nil
	}

	if msg.id == msg_bitfield {
		return bitfield(msg.payload), nil, nil
	}

	bf := make(bitfield, (num_pieces+7)/8)
	return bf, msg, nil
}

func (c *client) read() (*message, error) {
	return read_message(c.conn)
}

func (c *client) send_request(index, begin, length int) error {
	req := format_request(index, begin, length)
	_, err := c.conn.Write(req.serialize())
	return err
}

func (c *client) send_interested() error {
	msg := message{id: msg_interested}
	_, err := c.conn.Write(msg.serialize())
	return err
}

func (c *client) send_unchoke() error {
	msg := message{id: msg_unchoke}
	_, err := c.conn.Write(msg.serialize())
	return err
}