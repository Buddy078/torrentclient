# BitTorrent Client in Go

A high-performance, concurrent BitTorrent client written in Go following official BitTorrent Enhancement Proposals (BEP). It includes an interactive real-time terminal UI, download resumption with integrity verification, a seeder TCP server on port 6881, UDP tracker integration, Magnet URI parsing, and a multi-file directory storage router.

---

## Features

### Baseline Implementations
- **Core BitTorrent Wire Protocol (BEP 0003)**:
  - Canonical info-hash extraction via raw bencode slice parsing to avoid hash mismatches.
  - Full handshake serialization and remote peer validation.
  - Big-endian frame serialization for messages (`choke`, `unchoke`, `interested`, `have`, `bitfield`, `request`, `piece`).
  - Bitfield indexing with big-endian bit shifts and concurrent worker block pipelining.
  - Per-piece SHA-1 integrity checks prior to writing to disk.
- **Live Terminal Progress Dashboard (TUI)**:
  - In-place ANSI-clearing ticker showing real-time transfer speeds (KB/s or MB/s).
  - Dynamic completion percentage with a responsive ASCII progress bar.
  - Estimated time of arrival (ETA), transferred/total megabytes, active swarm peers, and piece ratios.
- **Download Resumption**:
  - Direct random-access disk writes (`WriteAt` and `ReadAt`) that bypass memory bloat.
  - Persisted state serialization to `<output>.state` mapping verified bitfields.
  - Pre-flight disk hash verification upon startup to resume interrupted downloads without redundant network requests.
- **Seeding / Peer Upload Handler**:
  - Concurrent TCP listener bound to port `6881`.
  - Automatic handshake verification, bitfield broadcast, unchoking, and on-demand block serving (`msg_piece`) from local storage.

### Bonus Extensions
- **UDP Tracker Protocol (BEP 0015)**:
  - Dual-action binary protocol over UDP (`actionConnect` and `actionAnnounce`).
  - Generates transaction IDs, handles connection ID reuse, and unpacks compact 6-byte peer representations.
- **Magnet Link Resolution (BEP 0009)**:
  - Parses `magnet:?` URIs containing `xt=urn:btih:`, tracker endpoints (`tr`), and display names (`dn`).
  - Allows starting downloads directly without a pre-downloaded `.torrent` file.
- **Multi-File Torrent Structure Parsing**:
  - Parses nested `files` lists inside the `info` bencode dictionary.
  - Maps continuous payload byte offsets across multiple subdirectories and files on disk.

---

## Project Structure

```text
torrent-client/
├── client.go         # Peer network connection, handshakes, and bitfield management
├── wire.go           # Low-level protocol framing, big-endian serialization, and message decoding
├── torrent.go        # Bencode parsing, canonical byte-level info-hash slicing, and tracker routing
├── storage.go        # Multi-file directory manager and offset-based disk I/O
├── p2p.go            # Worker pool orchestration, request pipelining, integrity check, and TUI dashboard
├── seeder.go         # Inbound TCP seeder on port 6881 for uploading blocks to the swarm
├── udp_tracker.go    # BEP 0015 UDP tracker connection and announce transactions
├── magnet.go         # BEP 0009 Magnet URI parsing and hash extraction
├── main.go           # CLI argument parsing and execution dispatch
├── go.mod            # Go module definitions
├── go.sum            # Module checksums
├── .gitignore        # Ignores binaries, downloaded images, and state files
└── README.md         # Documentation