package main

import (
	"encoding/binary"
	"fmt"
	"io"
)

type message_id uint8

const (
	msg_choke          message_id = 0
	msg_unchoke        message_id = 1
	msg_interested     message_id = 2
	msg_not_interested message_id = 3
	msg_have           message_id = 4
	msg_bitfield       message_id = 5
	msg_request        message_id = 6
	msg_piece          message_id = 7
	msg_cancel         message_id = 8
)

type message struct {
	id      message_id
	payload []byte
}

func (m *message) serialize() []byte {
	if m == nil {
		return make([]byte, 4) // keep-alive
	}
	length := uint32(len(m.payload) + 1)
	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], length)
	buf[4] = byte(m.id)
	copy(buf[5:], m.payload)
	return buf
}

func read_message(r io.Reader) (*message, error) {
	len_buf := make([]byte, 4)
	if _, err := io.ReadFull(r, len_buf); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(len_buf)
	if length == 0 {
		return nil, nil // keep-alive
	}

	msg_buf := make([]byte, length)
	if _, err := io.ReadFull(r, msg_buf); err != nil {
		return nil, err
	}

	return &message{
		id:      message_id(msg_buf[0]),
		payload: msg_buf[1:],
	}, nil
}

func format_request(index, begin, length int) *message {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], uint32(index))
	binary.BigEndian.PutUint32(payload[4:8], uint32(begin))
	binary.BigEndian.PutUint32(payload[8:12], uint32(length))
	return &message{id: msg_request, payload: payload}
}

func parse_have(msg *message) (int, error) {
	if msg.id != msg_have {
		return 0, fmt.Errorf("expected have (id 4), got %d", msg.id)
	}
	if len(msg.payload) != 4 {
		return 0, fmt.Errorf("expected payload length 4, got %d", len(msg.payload))
	}
	return int(binary.BigEndian.Uint32(msg.payload)), nil
}

func parse_piece(index int, buf []byte, msg *message) (int, error) {
	if msg.id != msg_piece {
		return 0, fmt.Errorf("expected piece (id 7), got %d", msg.id)
	}
	if len(msg.payload) < 8 {
		return 0, fmt.Errorf("payload too short: %d < 8", len(msg.payload))
	}

	parsed_index := int(binary.BigEndian.Uint32(msg.payload[0:4]))
	if parsed_index != index {
		return 0, fmt.Errorf("expected piece %d, got %d", index, parsed_index)
	}

	begin := int(binary.BigEndian.Uint32(msg.payload[4:8]))
	data := msg.payload[8:]

	if begin+len(data) > len(buf) {
		return 0, fmt.Errorf("piece data offset %d exceeds buffer size %d", begin+len(data), len(buf))
	}

	copy(buf[begin:], data)
	return len(data), nil
}