# circuit

Go package providing an implementation of the circuit-breaker pattern as described by [Martin
Fowler](https://martinfowler.com/bliki/CircuitBreaker.html)


[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/iand/circuit)
[![Check Status](https://github.com/iand/circuit/actions/workflows/check.yml/badge.svg)](https://github.com/iand/circuit/actions/workflows/check.yml)
[![Test Status](https://github.com/iand/circuit/actions/workflows/test.yml/badge.svg)](https://github.com/iand/circuit/actions/workflows/test.yml)

## Overview

The circuit breaker can be in one of three states: closed (requests will be executed normally), open
(requests will be rejected immediately) or half-open (a single request will be used to determine
whether to move to the open or closed states)

During normal operation the breaker is in the closed state. When a request fails a counter is
incremented. A successful request will reset the counter. When the failure counter reaches a
threshold, indicating a consecutive series of failures, the breaker will trip and move to the open
state.

In the open state all requests will fail immediately, returning the ErrCircuitOpen error.

A timer is started and after the reset timeout, the breaker will move into the half-open state. In
the half-open state the first call is used to trial the system. During this trial all other requests
will fail as though the breaker were in the open state. If the trialing request succeeds the breaker
is moved to the closed (normal) state. Otherwise the breaker moves back to the open state and the
reset timer is restarted.

In the closed and half-open states, a count of the number of concurrent requests is maintained. This
number rises above the configured maximum then the breaker will trip into the open state.

This implementation has run in a high volume production real time bidding environment for over a
year and has no currently known bugs.

## Installation

Simply run

	go get -u github.com/iand/circuit

Documentation is at [https://pkg.go.dev/github.com/iand/circuit](https://pkg.go.dev/github.com/iand/circuit)

## Author

* [Ian Davis](http://github.com/iand) - <http://iandavis.com/>

Note that this package was initially developed for [Avocet](https://github.com/avct) and is released here with their permission.

## License

This is free and unencumbered software released into the public domain. For more
information, see <http://unlicense.org/> or the accompanying [`UNLICENSE`](UNLICENSE) file.
