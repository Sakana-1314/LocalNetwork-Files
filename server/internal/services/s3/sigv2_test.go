package s3

import (
	"net/http/httptest"
	"testing"
)

// Test vectors from the AWS S3 SigV2 documentation ("Signing and
// authenticating REST requests").
const (
	docAK     = "AKIAIOSFODNN7EXAMPLE"
	docSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

func TestSigV2DocVectors(t *testing.T) {
	r := httptest.NewRequest("GET", "http://johnsmith.s3.amazonaws.com/photos/puppy.jpg", nil)
	r.Header.Set("Date", "Tue, 27 Mar 2007 19:36:42 +0000")
	p := &sigParams{v2: true, accessKey: docAK, signature: "bWq2s1WEIj+Ydj0vQ697zp+IXMU="}
	if !p.verifyV2(r, docSecret, "/johnsmith") {
		t.Fatal("GET object vector")
	}
	r = httptest.NewRequest("PUT", "http://johnsmith.s3.amazonaws.com/photos/puppy.jpg", nil)
	r.Header.Set("Date", "Tue, 27 Mar 2007 21:15:45 +0000")
	r.Header.Set("Content-Type", "image/jpeg")
	p = &sigParams{v2: true, accessKey: docAK, signature: "MyyxeRY7whkBe+bq8fHCL/2kKUg="}
	if !p.verifyV2(r, docSecret, "/johnsmith") {
		t.Fatal("PUT object vector")
	}
	r = httptest.NewRequest("GET", "http://johnsmith.s3.amazonaws.com/?prefix=photos&max-keys=50&marker=puppy", nil)
	r.Header.Set("Date", "Tue, 27 Mar 2007 19:42:41 +0000")
	r.Header.Set("User-Agent", "Mozilla/5.0")
	p = &sigParams{v2: true, accessKey: docAK, signature: "htDYFYduRNen8P9ZfE/s9SuKy0U="}
	if !p.verifyV2(r, docSecret, "/johnsmith") {
		t.Fatal("list vector")
	}
	r = httptest.NewRequest("GET", "http://johnsmith.s3.amazonaws.com/?acl", nil)
	r.Header.Set("Date", "Tue, 27 Mar 2007 19:44:46 +0000")
	p = &sigParams{v2: true, accessKey: docAK, signature: "c2WLPFtWHVgbEmeEG93a4cG37dM="}
	if !p.verifyV2(r, docSecret, "/johnsmith") {
		t.Fatal("acl vector")
	}
	// Presigned query string form.
	r = httptest.NewRequest("GET", "http://johnsmith.s3.amazonaws.com/photos/puppy.jpg?AWSAccessKeyId=AKIAIOSFODNN7EXAMPLE&Expires=1175139620&Signature=NpgCjnDzrM%2BWFzoENXmpNDUsSn8%3D", nil)
	p = &sigParams{v2: true, presigned: true, accessKey: docAK, signature: "NpgCjnDzrM+WFzoENXmpNDUsSn8=", v2Expires: "1175139620"}
	if !p.verifyV2(r, docSecret, "/johnsmith") {
		t.Fatal("presigned vector")
	}
	p.signature = "AAAACjnDzrM+WFzoENXmpNDUsSn8="
	if p.verifyV2(r, docSecret, "/johnsmith") {
		t.Fatal("bad signature accepted")
	}
}
