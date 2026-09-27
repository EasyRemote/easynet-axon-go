# Go Industrial Test Suite

This directory contains the 16 core per-SDK industrial capability tests.
The normative evidence catalog is `sdk/INDUSTRIAL_TEST_MATRIX.md`.

## Evidence Rules

- A skipped, ignored, or disabled test is not passing evidence.
- Tests must execute the SDK provider implementation.
- Shared lifecycle parity is determined by the machine-readable capability
  matrix and shared vector runners, not this README.
- Current status comes from the current test and gate run.

## Running locally

See the SDK's main `README.md` for the canonical command.
