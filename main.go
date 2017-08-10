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
	"os"
	"path"
	"strings"
	"time"
)

const (
	defaultServer   = "localhost"
	defaultPort     = "8081"
	defaultUsername = "admin"
	defaultPassword = "admin123"

	defaultArtifact = ""
	defaultVersion  = ""

	defaultRepository = "releases"

	defaultCount = 200
)

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
	var (
		// force deletion, display only otherwise
		delete     bool
		keepLatest bool

		// Expect exactly one artifact
		expect int

		// Limit actions
		throttle int

		// Nexus number of items to return
		count int

		// Nexus coordinates
		server     string
		port       string
		username   string
		password   string
		repository string

		// Search coordinates
		artifact string
		version  string
	)

	// Parse commandline parameter
	flag.IntVar(&count, "count", defaultCount,
		"Nexus count parameter in REST interface")
	flag.BoolVar(&delete, "delete", false,
		"delete search results (otherwise only display them)")
	flag.BoolVar(&keepLatest, "keepLatest", true, "keep lastest version")
	flag.IntVar(&expect, "expect", 1, "expected number of results")
	flag.IntVar(&throttle, "throttle", 1, "throttle number of actions")
	flag.StringVar(&server, "server", defaultServer, "Nexus server name")
	flag.StringVar(&port, "port", defaultPort, "Nexus port")
	flag.StringVar(&repository, "repository", defaultRepository, "Nexus repository ID, empty for global search")
	flag.StringVar(&username, "username", defaultUsername, "Nexus user")
	flag.StringVar(&password, "password", defaultPassword,
		"Nexus password")
	flag.StringVar(&artifact, "artifact", defaultArtifact,
		"Limit search to an artifact")
	flag.StringVar(&version, "version", defaultVersion,
		"Limit search to specific version (may include wildcards)")

	flag.Usage = usage
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
		gav := Gav{ArtifactID: artifact,
			GroupID: group,
			Version: version}
		log.Printf("processing %+v\n", gav)

		found := search(server, port, repository, gav, count)
		n := len(found.Artifacts)

		// Display on stdout
		for i := 0; i < n; i++ {
			fmt.Printf("%v\n", found.Artifacts[i].ConciseNotation())
		}
		log.Printf("search returned %v artifacts out of %v\n",
			n, found.TotalCount)
		// Check expected result size
		if n > expect {
			msg := "Found %v artifacts but expect is %v, aborting\n"
			fmt.Fprintf(os.Stderr, msg, n, expect)
			os.Exit(1)
		}
		for i := 0; i < n; i++ {
			a := found.Artifacts[i]
			if keepLatest && a.Version == a.LatestRelease {
				log.Printf("keeping latest version %v for %v\n",
					a.LatestRelease, a.ConciseNotation())
				continue
			}

			// Check if throttle limit reached
			if actions >= throttle {
				s := "throttle=%v reached, exiting"
				log.Printf(s, actions)
				os.Exit(0)
			}

			if delete {
				now := time.Now()
				deleted := deleteGav(server, port, repository,
					username, password, a)
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
func deleteGav(server, port, repository, username, password string, gav Gav) bool {
	s := "http://%s:%s/service/local/repositories/%s/content/%s"
	url := fmt.Sprintf(s, server, port, repository, gav.DefaultLayout())
	log.Printf("HTTP DELETE %v", url)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.SetBasicAuth(username, password)
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
		log.Printf("%s is already gone", gav.ConciseNotation())
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
func search(server string, port, repository string, gav Gav, count int) searchNGResponse {
	url := fmt.Sprintf("http://%s:%s"+
		"/service/local/lucene/search?"+
		"g=%s&count=%d",
		server, port, gav.GroupID, count)
	if repository != "" {
		url += fmt.Sprintf("&repositoryId=%s", repository)
	}
	if gav.ArtifactID != "" {
		url += fmt.Sprintf("&a=%s", gav.ArtifactID)
	}
	if gav.Version != "" {
		url += fmt.Sprintf("&v=%s", gav.Version)
	}
	response, err := http.Get(url)
	if err != nil {
		log.Fatalf("Cannot read url %v: %v\n", url, err)
	}
	log.Printf("%v returns HTTP status code %v\n",
		url, response.StatusCode)
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
