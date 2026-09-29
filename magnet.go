package main

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

type magnet_link struct {
	info_hash [20]byte
	name      string
	trackers  []string
}

func parse_magnet_link(uri string) (*magnet_link, error) {
	if !strings.HasPrefix(uri, "magnet:?") {
		return nil, fmt.Errorf("invalid magnet link prefix")
	}

	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	query := u.Query()
	xt := query.Get("xt")
	if xt == "" {
		return nil, fmt.Errorf("missing xt parameter in magnet link")
	}

	var infoHash [20]byte
	if strings.HasPrefix(xt, "urn:btih:") {
		hashStr := strings.TrimPrefix(xt, "urn:btih:")
		if len(hashStr) == 40 {
			rawHash, err := hex.DecodeString(hashStr)
			if err != nil {
				return nil, fmt.Errorf("invalid hex info hash: %w", err)
			}
			copy(infoHash[:], rawHash)
		} else {
			return nil, fmt.Errorf("unsupported info hash length: %d (expected 40-char hex)", len(hashStr))
		}
	} else {
		return nil, fmt.Errorf("unsupported xt prefix in magnet link")
	}

	name := query.Get("dn")
	if name == "" {
		name = "magnet_download"
	}

	trackers := query["tr"]

	return &magnet_link{
		info_hash: infoHash,
		name:      name,
		trackers:  trackers,
	}, nil
}