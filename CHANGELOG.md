# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Add Linux network repair for Wuji gloves and Hand 2 with adapter selection,
  sudo elevation, factory-IP ARP probes, host routes, rollback, and device checks.

- Add a terminal UI to scan and select Wuji gloves by handedness, serial number,
  and current firmware version.
- Add local OTA ZIP package validation, installation plan confirmation, and
  sequential firmware upgrades or downgrades through the official Wuji CLI.
- Add live installation reports and stop remaining devices after a failure.
- Add setup documentation, build commands, and automated workflow tests.
- Add grouped target descriptions through `make help` and make it the default
  target when running `make` without arguments.
- Add native Linux x86_64/ARM64 and Apple Silicon macOS CI checks for Go 1.22
  and stable Go, with race detection and a 70% coverage minimum.
- Add `make format-check` and `make build-cross` for Linux/macOS amd64 and ARM64
  builds, and document supported firmware-update hosts.
- Add `make install` to install missing Go and Wuji CLI dependencies through
  native shell installers before building or running the helper.

### Changed

- Open a task menu for firmware updates and network repair, with separate flows
  and main-menu navigation; allow repair without Wuji CLI installed.

- Rename the project, Go module, terminal title, and executable to Wuji Helper
  (`wuji-helper`).
- Redesign the firmware workflow with a consistent workspace, device details,
  inline package errors, explicit confirmation, and per-glove progress reports.
- Preserve selections and package paths when navigating back or rescanning.

### Fixed

- Fix arrow-key navigation across lists, forms, buttons, and report panels while
  preserving cursor movement inside the package path field.
