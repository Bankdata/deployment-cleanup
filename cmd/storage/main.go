package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

// RepositoryData Containing data on specific repository
type RepositoryData struct {
	orgName  string
	repoName string
	branches []string
}

var (
	ghToken string
	azName  string
	azKey   string
	config  map[string]RepositoryData
)

func main() {
	log.Print("Starting clean up run")

	ctx := context.Background()
	ghToken = os.Getenv("GITHUB_ACCESS_TOKEN")
	azName = os.Getenv("AZURE_STORAGE_ACCOUNT_NAME")
	azKey = os.Getenv("AZURE_STORAGE_ACCOUNT_KEY")

	config = make(map[string]RepositoryData)
	for _, slug := range os.Args[1:] {
		parts := strings.Split(slug, "/")
		config[parts[1]] = initializeRepositoryData(ctx, parts[0], parts[1])
	}

	credential, err := azblob.NewSharedKeyCredential(azName, azKey)
	if err != nil {
		log.Fatal(err)
	}

	for _, v := range config {
		handleRepo(ctx, credential, v)
	}
}

func handleRepo(ctx context.Context, credential azblob.Credential, repo RepositoryData) {
	log.Printf("Handling clean up for repository: %s/%s", repo.orgName, repo.repoName)

	u, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s-%s", azName, repo.orgName, repo.repoName))
	containerURL := azblob.NewContainerURL(*u, azblob.NewPipeline(credential, azblob.PipelineOptions{}))
	response, err := containerURL.ListBlobsFlatSegment(ctx, azblob.Marker{}, azblob.ListBlobsSegmentOptions{Details: azblob.BlobListingDetails{Metadata: true}})
	if err != nil {
		if stgErr, ok := err.(azblob.StorageError); ok {
			if stgErr.ServiceCode() == azblob.ServiceCodeContainerNotFound {
				log.Printf("No container for repository %s/%s", repo.orgName, repo.repoName)
				return
			}
		}
		log.Fatal(err)
	}

	for _, blob := range response.Segment.BlobItems {
		keep := false

		for _, branch := range repo.branches {
			if branch == blob.Metadata["branch"] {
				keep = true
			}
		}

		if !keep {
			log.Printf("Deleting %s", blob.Name)
			bu, _ := url.Parse(fmt.Sprintf("%s/%s", u.String(), blob.Name))
			blobURL := azblob.NewBlobURL(*bu, azblob.NewPipeline(credential, azblob.PipelineOptions{}))
			blobURL.Delete(ctx, azblob.DeleteSnapshotsOptionInclude, azblob.BlobAccessConditions{})
		}
	}
}

// Initialize data from repository
func initializeRepositoryData(ctx context.Context, orgName string, repoName string) RepositoryData {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: ghToken})
	tc := oauth2.NewClient(ctx, ts)
	gh := github.NewClient(tc)

	// Read branches
	listOpts := &github.ListOptions{}
	branches, _, err := gh.Repositories.ListBranches(ctx, orgName, repoName, listOpts)
	if err != nil {
		log.Fatalf("GH error %s", err)
	}
	branchNames := make([]string, len(branches)+1)
	for i, branch := range branches {
		branchNames[i] = *branch.Name
	}
	branchNames[len(branches)] = "master" // Protected branches not returned

	return RepositoryData{
		orgName:  orgName,
		repoName: repoName,
		branches: branchNames,
	}
}
