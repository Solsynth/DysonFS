package s3server

import "encoding/xml"

type ListBucketResult struct {
	XMLName     xml.Name      `xml:"ListBucketResult"`
	Name        string        `xml:"Name"`
	Prefix      string        `xml:"Prefix"`
	KeyCount    int           `xml:"KeyCount"`
	MaxKeys     int           `xml:"MaxKeys"`
	IsTruncated bool          `xml:"IsTruncated"`
	Contents    []ObjectEntry `xml:"Contents"`
}

type ObjectEntry struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	Size         int64  `xml:"Size"`
	ETag         string `xml:"ETag"`
	StorageClass string `xml:"StorageClass"`
}

type ListAllMyBucketsResult struct {
	XMLName xml.Name       `xml:"ListAllMyBucketsResult"`
	Owner   BucketOwner    `xml:"Owner"`
	Buckets []BucketEntry  `xml:"Buckets>Bucket"`
}

type BucketOwner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type BucketEntry struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

type Error struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource"`
	RequestID string   `xml:"RequestId"`
}

type CompleteMultipartUpload struct {
	XMLName xml.Name            `xml:"CompleteMultipartUpload"`
	Parts   []MultipartUploadPart `xml:"Part"`
}

type MultipartUploadPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type CompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

// ListPartsResult mirrors the S3 ListParts response so minio-go's
// ListObjectParts client call can round-trip against the mock.
type ListPartsResult struct {
	XMLName     xml.Name         `xml:"ListPartsResult"`
	Bucket      string           `xml:"Bucket"`
	Key         string           `xml:"Key"`
	UploadID    string           `xml:"UploadId"`
	Parts       []ListPartsEntry `xml:"Part"`
	IsTruncated bool             `xml:"IsTruncated"`
}

type ListPartsEntry struct {
	PartNumber   int    `xml:"PartNumber"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
}

type InitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}
