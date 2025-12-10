package buildcontext

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/radiofrance/dib/internal/logger"
)

// AzureBlobUploader implements the FileUploader interface to upload files to Azure Blob Storage.
type AzureBlobUploader struct {
	client      *azblob.Client
	accountName string
	accountKey  string
	container   string
}

// NewAzureBlobUploader creates a new instance of AzureBlobUploader.
func NewAzureBlobUploader(accountName, accountKey, container string) (*AzureBlobUploader, error) {
	cred, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials: %w", err)
	}

	url := fmt.Sprintf("https://%s.blob.core.windows.net/", accountName)

	client, err := azblob.NewClientWithSharedKeyCredential(url, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return &AzureBlobUploader{
		client:      client,
		accountName: accountName,
		accountKey:  accountKey,
		container:   container,
	}, nil
}

// UploadFile uploads a file to the specified container and path in Azure Blob Storage.
func (u *AzureBlobUploader) UploadFile(ctx context.Context, filePath, targetPath string) error {
	file, err := os.Open(filePath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("can't open file %s: %w", filePath, err)
	}

	defer func() {
		err = file.Close()
		if err != nil {
			logger.Errorf("can't close file %s: %v", filePath, err)
		}
	}()

	_, err = u.client.UploadFile(ctx, u.container, targetPath, file, nil)
	if err != nil {
		return fmt.Errorf("failed to upload file to Azure Blob Storage: %w", err)
	}

	return nil
}

// PresignedURL generates a SAS URL for accessing a blob in Azure Blob Storage.
func (u *AzureBlobUploader) PresignedURL(ctx context.Context, targetPath string) (string, error) {
	cred, err := azblob.NewSharedKeyCredential(u.accountName, u.accountKey)
	if err != nil {
		return "", fmt.Errorf("invalid credentials for SAS generation: %w", err)
	}

	sasQueryParams, err := sas.BlobSignatureValues{
		Protocol:      sas.ProtocolHTTPS,
		StartTime:     time.Now().UTC().Add(-15 * time.Minute), // Adjust for clock skew
		ExpiryTime:    time.Now().UTC().Add(1 * time.Hour),
		Permissions:   (&sas.BlobPermissions{Read: true}).String(),
		ContainerName: u.container,
		BlobName:      targetPath,
	}.SignWithSharedKey(cred)
	if err != nil {
		return "", fmt.Errorf("failed to generate SAS token: %w", err)
	}

	url := fmt.Sprintf("https://%s.blob.core.windows.net/%s/%s?%s",
		u.accountName, u.container, targetPath, sasQueryParams.Encode())

	return url, nil
}
