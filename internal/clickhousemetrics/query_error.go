package clickhousemetrics

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ClickHouse server error codes the collector branches on
// (src/Common/ErrorCodes.cpp).
const (
	// codeNoElementsInConfig is what 26.8 raises for system.zookeeper and
	// system.zookeeper_connection on a node with no <zookeeper>, <keeper> or
	// clustered <keeper_server> section: Context::getZooKeeper calls
	// zkutil::getZooKeeperConfigName, which throws
	// "There is no Zookeeper configuration in server config".
	codeNoElementsInConfig = 139
	// codeNoZooKeeper (NO_ZOOKEEPER) is the dedicated "Keeper not configured"
	// code, raised e.g. when creating a Replicated table without Keeper.
	codeNoZooKeeper = 225
	// codeKeeperException (KEEPER_EXCEPTION) is every Coordination::Exception
	// from the Keeper client: connection loss, all connection tries failed,
	// session expired, operation timeout. The client only exists when Keeper
	// is configured, so this code means configured but not serving.
	codeKeeperException = 999
)

// queryError is a non-200 answer from the ClickHouse HTTP interface.
type queryError struct {
	HTTPStatus int
	// Code is the ClickHouse exception code, 0 when the reply carried none
	// (e.g. a proxy error page).
	Code int
	// Message is the start of the response body (the exception text).
	Message string
}

// Error keeps the format the collector has always logged.
func (e *queryError) Error() string {
	return fmt.Sprintf("clickhouse http %d: %s", e.HTTPStatus, e.Message)
}

// exceptionCodeInBody matches the "Code: 139. DB::Exception: ..." prefix of a
// ClickHouse exception message.
var exceptionCodeInBody = regexp.MustCompile(`\bCode: (\d+)\.`)

// parseExceptionCode reads the exception code from the
// X-ClickHouse-Exception-Code header, which ClickHouse sets whenever the
// exception is raised before any output is written, and falls back to the
// "Code: N." prefix of the body. 0 when neither carries a code.
func parseExceptionCode(header, body string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && n > 0 {
		return n
	}
	if m := exceptionCodeInBody.FindStringSubmatch(body); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return 0
}
