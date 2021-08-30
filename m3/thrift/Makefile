gen-thrift:
	@thrift --gen go:thrift_import="github.com/uber-go/tally/v4/thirdparty/github.com/apache/thrift/lib/go/thrift" -out . v1/v1.thrift && rm -rf v1/m3-remote
	@echo Generated v1 Go Thrift in metrics/m3/thrift/v1.
	@thrift --gen go:thrift_import="github.com/uber-go/tally/v4/thirdparty/github.com/apache/thrift/lib/go/thrift" -out . v2/v2.thrift && rm -rf v2/m3-remote
	@echo Generated v2 Go Thrift in metrics/m3/thrift/v2.
	@git apply thrift.diff
	@echo Applied thrift.diff.
