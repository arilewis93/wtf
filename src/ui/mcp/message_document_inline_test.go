package mcp

import (
	"encoding/base64"
	"testing"

	domainMessage "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/message"
	mcpg "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fakePDFBytes = []byte("%PDF-1.4 not a real pdf, just needs to be non-empty bytes")

func TestDownloadMediaInlinesASmallDocument(t *testing.T) {
	path := writeTempImage(t, fakePDFBytes, "contract.pdf")
	svc := &downloadMediaStub{resp: domainMessage.DownloadMediaResponse{
		MessageID: "M1", MediaType: "document", Filename: "contract.pdf", FilePath: path,
		FileSize: int64(len(fakePDFBytes)), FileURL: "https://example.test/files/d1/contract.pdf",
	}}
	h := InitMcpMessage(svc, &stubResolver{})

	res, err := h.handleMessage(deviceCtx(), callReq(map[string]any{
		"action": "download_media", "phone": "628", "message_id": "M1",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError)

	var found *mcpg.EmbeddedResource
	for _, c := range res.Content {
		if r, ok := mcpg.AsEmbeddedResource(c); ok {
			found = r
		}
	}
	require.NotNil(t, found, "expected an inline embedded-resource content block")
	blob, ok := mcpg.AsBlobResourceContents(found.Resource)
	require.True(t, ok)
	assert.Equal(t, "application/pdf", blob.MIMEType)
	decoded, err := base64.StdEncoding.DecodeString(blob.Blob)
	require.NoError(t, err)
	assert.Equal(t, fakePDFBytes, decoded)
}

func TestDownloadMediaSkipsInliningADocumentOverTheSizeCap(t *testing.T) {
	path := writeTempImage(t, fakePDFBytes, "contract.pdf")
	svc := &downloadMediaStub{resp: domainMessage.DownloadMediaResponse{
		MessageID: "M1", MediaType: "document", Filename: "contract.pdf", FilePath: path,
		FileSize: maxInlineDocumentBytes + 1, // claims to be over the cap, even though the real file is tiny
	}}
	h := InitMcpMessage(svc, &stubResolver{})

	res, err := h.handleMessage(deviceCtx(), callReq(map[string]any{
		"action": "download_media", "phone": "628", "message_id": "M1",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError)

	for _, c := range res.Content {
		_, ok := mcpg.AsEmbeddedResource(c)
		assert.False(t, ok, "an over-cap document must not be inlined, regardless of the real file's size")
	}
}

func TestDownloadMediaSkipsInliningForNonDocumentMedia(t *testing.T) {
	path := writeTempImage(t, onePixelPNG, "a.png")
	svc := &downloadMediaStub{resp: domainMessage.DownloadMediaResponse{
		MessageID: "M1", MediaType: "image", Filename: "a.png", FilePath: path, FileSize: int64(len(onePixelPNG)),
	}}
	h := InitMcpMessage(svc, &stubResolver{})

	res, err := h.handleMessage(deviceCtx(), callReq(map[string]any{
		"action": "download_media", "phone": "628", "message_id": "M1",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError)

	for _, c := range res.Content {
		_, ok := mcpg.AsEmbeddedResource(c)
		assert.False(t, ok, "an image must still inline as ImageContent, never as a generic blob resource")
	}
}

func TestDownloadMediaFallsBackCleanlyWhenTheDocumentIsMissing(t *testing.T) {
	svc := &downloadMediaStub{resp: domainMessage.DownloadMediaResponse{
		MessageID: "M1", MediaType: "document", Filename: "gone.pdf",
		FilePath: "/does/not/exist/gone.pdf", // never written
		FileSize: 123,
	}}
	h := InitMcpMessage(svc, &stubResolver{})

	res, err := h.handleMessage(deviceCtx(), callReq(map[string]any{
		"action": "download_media", "phone": "628", "message_id": "M1",
	}))
	require.NoError(t, err)
	assert.False(t, res.IsError, "a read failure falls back to the structured response, not an error")
}

func TestDownloadMediaInlinesAnUnrecognisedDocumentExtensionAsOctetStream(t *testing.T) {
	path := writeTempImage(t, fakePDFBytes, "export.dat")
	svc := &downloadMediaStub{resp: domainMessage.DownloadMediaResponse{
		MessageID: "M1", MediaType: "document", Filename: "export.dat", FilePath: path, FileSize: int64(len(fakePDFBytes)),
	}}
	h := InitMcpMessage(svc, &stubResolver{})

	res, err := h.handleMessage(deviceCtx(), callReq(map[string]any{
		"action": "download_media", "phone": "628", "message_id": "M1",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError)

	var found *mcpg.EmbeddedResource
	for _, c := range res.Content {
		if r, ok := mcpg.AsEmbeddedResource(c); ok {
			found = r
		}
	}
	require.NotNil(t, found)
	blob, ok := mcpg.AsBlobResourceContents(found.Resource)
	require.True(t, ok)
	assert.Equal(t, "application/octet-stream", blob.MIMEType)
}
