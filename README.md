# BitTorrent Client

A lightweight BitTorrent client written in Go.

## Use

- Build the project: `go build -o bin/torrent-client ./cmd/torrent-client/`
- Run with a torrent file: `./bin/torrent-client big-buck-bunny.torrent`
- Run with a magnet link: `./bin/torrent-client "magnet:?xt=urn:btih:..."`

## Report Issues

If you find a bug or have a problem, please open an issue on GitHub:

- `https://github.com/Abhicodeitout/Bit_torrent/issues`

When reporting an issue, include:
- a short summary
- the command you ran
- expected behavior
- actual behavior
- your environment (OS, Go version)

## Contributing

This repository is intended as a stable source for users. Direct modifications should be made through forks and pull requests, and only maintainers with write access can merge changes.
