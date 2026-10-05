package imagecache

import (
	"strings"
	"testing"
	"time"
)

// AWS のドキュメントにある presigned URL の例 (Authenticating Requests: Using
// Query Parameters → "An Example")。virtual-hosted style なので bucket は空。
func TestSigV4MatchesAWSExample(t *testing.T) {
	s := sigV4{
		host:      "examplebucket.s3.amazonaws.com",
		accessKey: "AKIAIOSFODNN7EXAMPLE",
		secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		region:    "us-east-1",
	}
	got := s.presign("test.txt", time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC), 86400*time.Second)
	want := "X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("got %s\nwant suffix %s", got, want)
	}
	if !strings.HasPrefix(got, "https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request&") {
		t.Fatalf("got %s", got)
	}
}
