package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	// Import to initialize client auth plugins.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/helm/pkg/helm"
	helm_env "k8s.io/helm/pkg/helm/environment"
	"k8s.io/helm/pkg/helm/portforwarder"
	"k8s.io/helm/pkg/kube"
	"k8s.io/helm/pkg/tlsutil"
)

// RepositoryData Containing data on specific repository
type RepositoryData struct {
	orgName      string
	branches     []string
	pullRequests []int
}

var (
	ghToken      string
	tillerTunnel *kube.Tunnel
	settings     helm_env.EnvSettings
	client       helm.Interface
	config       map[string]RepositoryData
)

func main() {
	log.Print("Starting Helm clean up run")

	ctx := context.Background()
	ghToken = os.Getenv("GITHUB_ACCESS_TOKEN")

	config = make(map[string]RepositoryData)
	for _, slug := range os.Args[1:] {
		parts := strings.Split(slug, "/")
		config[parts[1]] = initializeRepositoryData(ctx, parts[0], parts[1])
	}

	settings.TillerNamespace = "kube-system"
	settings.TillerConnectionTimeout = int64(300)

	setupConnection()
	client = ensureHelmClient(client)

	opts := []helm.ReleaseListOption{}
	res, err := client.ListReleases(opts...)
	if err != nil {
		log.Println(err)
	}

	releases := res.GetReleases()
	for _, release := range releases {
		parts := strings.Split(release.Name, "-")
		if len(parts) > 1 {
			repoName := parts[0]
			repositoryData, ok := config[repoName]

			if ok {
				var match = false

				// Check branches
				for _, branch := range repositoryData.branches {
					releaseName := releaseName(repoName, branch)
					log.Printf("comparing releaseName[%s] with release.Name[%s] based on repo[%s] and branch[%s]", releaseName, release.Name, repoName, branch)
					if releaseName == release.Name {
						match = true
					}
				}

				// Check pull requests
				for _, pr := range repositoryData.pullRequests {
					releaseName := releaseName(repoName, "pr-"+strconv.Itoa(pr))
					if releaseName == release.Name {
						match = true
					}
				}

				if !match {
					deleteOpts := []helm.DeleteOption{
						helm.DeletePurge(true),
					}
					response, err := client.DeleteRelease(release.Name, deleteOpts...)
					if err != nil {
						log.Println(err)
					} else {
						log.Printf("Deleted release [%s]: %s", release.Name, response.Info)
					}
				}

			}
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
	branchNames := make([]string, len(branches))
	for i, branch := range branches {
		branchNames[i] = *branch.Name
	}

	// Read pull requests
	prOpts := &github.PullRequestListOptions{}
	prs, _, err := gh.PullRequests.List(ctx, orgName, repoName, prOpts)
	if err != nil {
		log.Fatalf("GH error %s", err)
	}
	pullRequestIDs := make([]int, len(prs))
	for i, pr := range prs {
		pullRequestIDs[i] = *pr.Number
	}

	return RepositoryData{
		orgName:      orgName,
		branches:     branchNames,
		pullRequests: pullRequestIDs,
	}
}

// Generate release name using same algorithm as build pipeline! - try to make this metadata based in the future
func releaseName(repo string, branch string) string {
	var re = regexp.MustCompile("[^a-zA-Z0-9]+")
	s := re.ReplaceAllString(branch, "-")
	releaseName := repo + "-" + strings.ToLower(s)
	if len(releaseName) > 53 {
		return releaseName[0:53]
	}
	return releaseName
}

func setupConnection() error {
	if settings.TillerHost == "" {
		config, client, err := getKubeClient(settings.KubeContext, settings.KubeConfig)
		if err != nil {
			return err
		}

		tillerTunnel, err = portforwarder.New(settings.TillerNamespace, client, config)
		if err != nil {
			return err
		}
		log.Println("Port forward created")

		settings.TillerHost = fmt.Sprintf("127.0.0.1:%d", tillerTunnel.Local)
		log.Printf("Created tunnel using local port: '%d'\n", tillerTunnel.Local)
	}

	// Set up the gRPC config.
	log.Printf("SERVER: %q\n", settings.TillerHost)

	// Plugin support.
	return nil
}

// getKubeClient creates a Kubernetes config and client for a given kubeconfig context.
func getKubeClient(context string, kubeconfig string) (*rest.Config, kubernetes.Interface, error) {
	config, err := configForContext(context, kubeconfig)
	if err != nil {
		return nil, nil, err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("could not get Kubernetes client: %s", err)
	}
	log.Println("Client created")
	return config, client, nil
}

// configForContext creates a Kubernetes REST client configuration for a given kubeconfig context.
func configForContext(context string, kubeconfig string) (*rest.Config, error) {
	config, err := kube.GetConfig(context, kubeconfig).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("could not get Kubernetes config for context %q: %s", context, err)
	}
	return config, nil
}

// ensureHelmClient returns a new helm client impl. if h is not nil.
func ensureHelmClient(h helm.Interface) helm.Interface {
	if h != nil {
		return h
	}
	return newClient()
}

func newClient() helm.Interface {
	options := []helm.Option{helm.Host(settings.TillerHost), helm.ConnectTimeout(settings.TillerConnectionTimeout)}

	if settings.TLSVerify || settings.TLSEnable {
		log.Printf("Host=%q, Key=%q, Cert=%q, CA=%q\n", settings.TLSServerName, settings.TLSKeyFile, settings.TLSCertFile, settings.TLSCaCertFile)
		tlsopts := tlsutil.Options{
			ServerName:         settings.TLSServerName,
			KeyFile:            settings.TLSKeyFile,
			CertFile:           settings.TLSCertFile,
			InsecureSkipVerify: true,
		}
		if settings.TLSVerify {
			tlsopts.CaCertFile = settings.TLSCaCertFile
			tlsopts.InsecureSkipVerify = false
		}
		tlscfg, err := tlsutil.ClientConfig(tlsopts)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		options = append(options, helm.WithTLS(tlscfg))
	}
	return helm.NewClient(options...)
}
