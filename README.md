# Wuji Firmware Bouncer

A terminal UI to upgrade or downgrade selected Wuji gloves using an OTA ZIP
package supplied by Wuji, such as `~/gboard-v0.10.1.ota.zip`.

It uses the [official Wuji CLI](https://github.com/wuji-technology/wuji-cli)
for USB/network discovery, device communication, package verification, and
flashing. Firmware transport is not reimplemented here.

## Setup

Requires Go 1.22 or newer, a terminal, and a recent Wuji CLI supporting
`upgrade --file`. Development was verified with Wuji CLI `2026.9.22`.
Install the CLI following its official instructions:

```sh
curl -fsSL https://get.wuji.tech/cli | bash
wuji --version
make build
./bin/wuji-firmware-bouncer
```

Alternatively, run `make run`, or specify an executable explicitly:

```sh
go run . --wuji /path/to/wuji
```

Power on the gloves and connect them via USB or the same local network.

## Workflow

1. The tool scans devices, probes each serial, and reads `hand_side` and
   `firmware_version`. Each selectable glove shows **left/right handedness,
   serial number, and current firmware version**, plus its transport/address.
   Failed probes or metadata reads are shown as unavailable; other device types
   cannot be selected. Duplicate serials are listed once.
2. Use **Tab** to move, **Space** to select one or more gloves, and **Enter**
   on Next. Choose Rescan after connecting additional gloves.
3. Enter the package path. `~/`, relative paths, spaces, and paths pasted with
   surrounding quotes are supported. The tool validates ZIP integrity and keeps
   a private temporary copy. ZIP packages and their expanded contents are
   limited to 512 MiB. Raw `.bin` files are not accepted.
4. Review the selected gloves, their current firmware, the original path,
   declared package version (when available), size, and SHA-256 fingerprint.
   Use Page Up/Page Down to scroll the plan.
   Choose **Confirm & install** to authorize installing the package, including
   downgrades. Back edits the package; Cancel exits without flashing.
5. The tool rechecks each glove's type, handedness, and firmware before writing,
   then calls `wuji upgrade --sn SERIAL --file SNAPSHOT --yes` sequentially.
   The official CLI verifies the package manifest and firmware digest, and checks
   the declared device type when present. Keep gloves powered and connected;
   release other applications holding a direct glove connection.
6. Watch the CLI's live progress and per-device report. The operation stops on
   the first failure, leaving remaining gloves untouched. Successfully flashed
   gloves reboot. An earlier successfully flashed glove is not rolled back if
   a later glove fails. Same-version packages may be reported as skipped.

An older package downgrades firmware; a newer package upgrades it. This tool
installs your local package and does not choose or download a catalog version.
The displayed manifest version is metadata; the CLI performs firmware validation.
For upstream behavior, see [Wuji's firmware documentation](https://docs.wuji.tech/docs/en/wuji-cli/latest/firmware-upgrade/).

Ctrl+C exits before installation. During installation, the TUI ignores Ctrl+C
until the CLI finishes, to avoid interrupting an active flash. On the report
screen, scroll with arrow/Page Up/Page Down keys and press Enter or `q` to exit.
A failed operation exits with status 1. Temporary package copies are removed on
normal exit. After a forced process termination, leftover copies may remain in
the system temporary directory under `wuji-bouncer-*`.

## Development

```sh
make format
make test
make build
```

Tests cover package validation and snapshots, path expansion, device metadata,
explicit serial targeting, subprocess failures, and simulated terminal workflows
for confirmation, cancellation, and stopping a multi-glove plan after failure.
They use a fake CLI and do not flash hardware. Real device discovery/flashing
still needs validation with connected Wuji gloves and an official OTA package.
Build output is stored in the ignored `bin/` directory; `make clean` removes it.
