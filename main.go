//usr/bin/env go run $0 "$@"; exit

// Nexus Continuous Delivery Victor
// Cleans all but the latest release for a GAV
// return codes:
//  -1: number of artifacts found exceeds expected result size

package main

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

const (
	defaultProtocol    = "http"
	defaultServer      = "localhost"
	defaultPort        = "8081"
	defaultContextroot = "nexus/"
	defaultUsername    = "admin"
	defaultPassword    = "admin123"

	defaultArtifact = ""
	defaultVersion  = ""

	defaultRepository = "releases"

	defaultCount = 200
)

// NexusInstance holds coordinates of a Nexus installation
type NexusInstance struct {
	Protocol    string
	Server      string
	Port        string
	Contextroot string
	Username    string
	Password    string
}

// NexusRepository holds coordinates of a Nexus repository
type NexusRepository struct {
	NexusInstance
	RepositoryID string
}

func baseUrl(repo NexusRepository) *url.URL {
	s := fmt.Sprintf("%s://%s:%s/%s",
		repo.Protocol, repo.Server, repo.Port, repo.Contextroot)
	log.Printf("base URL: %s\n", s)
	u, err := url.Parse(s)
	if err != nil {
		log.Fatalf("cannot parse URL %s: %v\n", s, err)
	}
	return u
}

// Fqa holds coordinates to a fully qualified artifact
type Fqa struct {
	NexusRepository
	Gav
}

type searchNGResponse struct {
	Count          int   `xml:"count"`
	From           int   `xml:"from"`
	TotalCount     int   `xml:"totalCount"`
	TooManyResults bool  `xml:"tooManyResults"`
	Artifacts      []Gav `xml:"data>artifact"`
}

// Gav is the standard Maven coordinates: group, artifact, version, extended by
// a custom Nexus field for the latest release version
type Gav struct {
	GroupID       string `xml:"groupId"`
	ArtifactID    string `xml:"artifactId"`
	Version       string `xml:"version"`
	LatestRelease string `xml:"latestRelease"`
}

// ConciseNotation returns group:artifact:version
func (a Gav) ConciseNotation() string {
	return fmt.Sprintf("%v:%v:%v",
		a.GroupID,
		a.ArtifactID,
		a.Version)
}

// DefaultLayout supports group, artifact and version in GAVs.
// For complete GAV support including extension and classifier, the whole
// XML result has to be parsed. This is not necessary for our use case.
func (a Gav) DefaultLayout() string {
	return fmt.Sprintf("%s/%s/%s",
		strings.Replace(a.GroupID, ".", "/", -1),
		a.ArtifactID,
		a.Version)
}

func main() {
	flag.Usage = usage
	var repo NexusRepository
	flag.StringVar(&repo.Protocol, "protocol", defaultProtocol,
		"Nexus protocol (http/https)")
	flag.StringVar(&repo.Server, "server", defaultServer,
		"Nexus server name")
	flag.StringVar(&repo.Port, "port", defaultPort, "Nexus port")
	flag.StringVar(&repo.Contextroot, "contextroot", defaultContextroot,
		"Nexus context root")
	flag.StringVar(&repo.Username, "username", defaultUsername,
		"Nexus credentials")
	flag.StringVar(&repo.Password, "password", defaultPassword,
		"Nexus credentials")
	flag.StringVar(&repo.RepositoryID, "repository", defaultRepository,
		"Nexus repository ID, empty for global search")

	// Parse commandline parameter
	count := flag.Int("count", defaultCount,
		"Nexus count parameter in REST interface")
	delete := flag.Bool("delete", false,
		"delete search results (otherwise only display them)")
	keepLatest := flag.Bool("keepLatest", true, "keep lastest version")
	expect := flag.Int("expect", 1, "expected number of results")
	throttle := flag.Int("throttle", 1, "throttle number of actions")
	artifact := flag.String("artifact", defaultArtifact,
		"Limit search to an artifact")
	version := flag.String("version", defaultVersion,
		"Limit search to specific version (may include wildcards)")
	flag.Parse()

	// Need at least one group, or exactly one @filename
	if len(flag.Args()) == 0 {
		flag.Usage()
	}
	groups := resolveGroups()

	// If multiple instances are running, avoid syncing on generating Maven
	// metadata
	rand.Seed(time.Now().UnixNano())
	Shuffle(groups)

	actions := 0
	truncated := false
	perf := []int{}
	for _, group := range groups {
		gav := Gav{ArtifactID: *artifact,
			GroupID: group,
			Version: *version}
		log.Printf("processing %+v\n", gav)

		found := search(Fqa{repo, gav}, *count)
		n := len(found.Artifacts)

		// Display on stdout
		for i := 0; i < n; i++ {
			fmt.Printf("%v\n", found.Artifacts[i].ConciseNotation())
		}
		log.Printf("search returned %v artifacts out of %v\n",
			n, found.TotalCount)
		// Check expected result size
		if n > *expect {
			msg := "Found %v artifacts but expect is %v, aborting\n"
			fmt.Fprintf(os.Stderr, msg, n, *expect)
			os.Exit(1)
		}
		for i := 0; i < n; i++ {
			a := found.Artifacts[i]
			if *keepLatest && a.Version == a.LatestRelease {
				log.Printf("keeping latest version %v for %v\n",
					a.LatestRelease, a.ConciseNotation())
				continue
			}

			// Check if throttle limit reached
			if actions >= *throttle {
				s := "throttle=%v reached, exiting"
				log.Printf(s, actions)
				os.Exit(0)
			}

			if *delete {
				now := time.Now()
				deleted := deleteGav(Fqa{repo, a})
				if deleted {
					actions++

					elapsed := time.Since(now)
					// normalize ns (1e-9) to ms (1e-3)
					ms := int(elapsed.Nanoseconds() / 1e6)
					perf = append(perf, ms)
					log.Printf("[PERF] %v"+
						", average: %v ms"+
						", median: %v ms"+
						"\n",
						elapsed,
						ArithmeticMean(perf),
						Median(perf))
				}
			}
		}
		// Sometimes Nexus returns 200 (maximum) out of 473, sometimes
		// 73 out of 247 items for whatever reason
		if found.TooManyResults || (n != found.TotalCount) {
			truncated = true
		}
	}
	if truncated {
		log.Printf("Truncated batch, consider re-running")
	}
}

// delete removes artifacts via REST from Nexus
// DELETE on http://${server}:${port}/service/local/repositories/${repo}/
// content/${group}/${artifact}/${version}/${artifact}-${version}.jar
func deleteGav(fqa Fqa) bool {
	u := baseUrl(fqa.NexusRepository).String()
	u += fmt.Sprintf("%s/service/local/repositories/%s/content/%s",
		u, fqa.RepositoryID, fqa.Gav.DefaultLayout())
	log.Printf("HTTP DELETE %v", u)
	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.SetBasicAuth(fqa.NexusInstance.Username, fqa.NexusInstance.Password)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	rc := resp.StatusCode
	if rc == 204 {
		return true
	}
	if rc == 404 {
		// Nexus is bent out of shape
		log.Printf("%s is already gone", fqa.Gav.ConciseNotation())
		return false
	}
	log.Fatalf("Expected HTTP status code 204 but got %v", rc)
	var mu bool
	return mu
}

func groupIsFile(group string) bool {
	return strings.HasPrefix(group, "@")
}

// read groups from file
func read(filename string) (groups []string, err error) {
	buf, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	for scanner.Scan() {
		groups = append(groups, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return
}

func resolveGroups() []string {
	if groupIsFile(flag.Arg(0)) {
		if len(flag.Args()) > 1 {
			flag.Usage()
		}
		groups, err := read(flag.Arg(0)[1:])
		if err != nil {
			log.Fatal(err)
		}
		return groups
	}
	return flag.Args()
}

// search executes a REST search against Nexus
func search(fqa Fqa, count int) searchNGResponse {
	var sb strings.Builder
	sb.WriteString(baseUrl(fqa.NexusRepository).String())
	sb.WriteString(
		fmt.Sprintf("service/local/lucene/search?g=%s&count=%d",
			fqa.Gav.GroupID, count))
	if fqa.RepositoryID != "" {
		sb.WriteString(
			fmt.Sprintf("&repositoryId=%s",
				fqa.RepositoryID))
	}
	if fqa.Gav.ArtifactID != "" {
		sb.WriteString(fmt.Sprintf("&a=%s", fqa.Gav.ArtifactID))
	}
	if fqa.Gav.Version != "" {
		sb.WriteString(fmt.Sprintf("&v=%s", fqa.Gav.Version))
	}
	u := sb.String()
	response, err := http.Get(u)
	if err != nil {
		log.Fatalf("Cannot read url %v: %v\n", u, err)
	}
	log.Printf("%v returns HTTP status code %v\n",
		u, response.StatusCode)
	if response.StatusCode != 200 {
		log.Fatalf("Expected status 200 but got %v\n",
			response.StatusCode)
	}
	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatal(err)
	}
	var found searchNGResponse
	err = xml.Unmarshal(body, &found)
	if err != nil {
		log.Fatal(err)
	}
	return found
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [options] [group...|@filename]\n",
		path.Base(os.Args[0]))
	fmt.Fprintf(os.Stderr, "\t@ references a file with groups separated "+
		" by newline\n")
	flag.PrintDefaults()
}
