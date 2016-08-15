# :heavy_check_mark: tally [![GoDoc][doc-img]][doc] [![Build Status][ci-img]][ci] [![Coverage Status][cov-img]][cov]

Fast, buffered, heirarchical stats collection in Go.

## Installation
`go get -u github.com/uber-go/tally`

## Structure

Tally provides a common interface for tracking metrics, while letting
you not worry about the velocity of logging.

```
Code example...
```

## Performance

Something, something, something, dark side

## Development Status: Beta
Ready for adventurous users, but we're planning several breaking changes before
releasing version 1.0. [This milestone][v1] tracks our progress toward a stable
release.

<hr>
Released under the [MIT License](LICENSE).

<sup id="footnote-versions">1</sup> In particular, keep in mind that we may be
benchmarking against slightly older versions of other libraries. Versions are
pinned in tally [glide.lock][] file. [↩](#anchor-versions)

[doc-img]: https://godoc.org/github.com/uber-go/tally?status.svg
[doc]: https://godoc.org/github.com/uber-go/tally
[ci-img]: https://travis-ci.org/uber-go/tally.svg?branch=master
[ci]: https://travis-ci.org/uber-go/tally
[cov-img]: https://coveralls.io/repos/github/uber-go/tally/badge.svg?branch=master
[cov]: https://coveralls.io/github/uber-go/tally?branch=master
[glide.lock]: https://github.com/uber-go/tally/blob/master/glide.lock
[v1]: https://github.com/uber-go/tally/milestones
