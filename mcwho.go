//
// Copyright 2012-2014 Geoff Hickey <trapgate@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

//
// mcwho: The program that answers the question, "who's on minecraft?"
//
// This program uses fsnotify to replace exp/inotify. Run 'go get
// github.com/howeyc/fsnotify' before compiling.
//
package main

import (
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/howeyc/fsnotify"
)

type mcuser struct {
	name  string    // user name
	since time.Time // time logged on or off
}

type userList map[string]mcuser

// @@TODO: Lock these, or copy them to the rss goroutine
var usersOn userList
var usersOff userList

// Command-line flags
var logpath = flag.String("log-path", "logs/", "the location of the Minecraft server.log files")

func main() {
	flag.Parse()

	// Make the maps
	usersOn = make(userList)
	usersOff = make(userList)

	// Channels to communicate with the goroutine that watches the minecraft logfile:
	conch := make(chan mcuser)
	disch := make(chan mcuser)
	errch := make(chan error)

	var user mcuser

	// Start up our RSS server
	go startRssServer()

	// Now start up the logfile watcher
	go Mcwho(*logpath, conch, disch, errch)
	for {
		select {
		case user = <-conch:
			delete(usersOff, user.name)
			usersOn[user.name] = user
		case user = <-disch:
			delete(usersOn, user.name)
			usersOff[user.name] = user
		case err := <-errch:
			log.Fatal(err)
		}
	}
}

//
// From the list of users, make the strings for display. The returned string
// is of the format, "3 players: happy on for 1h, dopey on for 32s, lucky on
// for 2d"
//
func getDisplay(usersOn userList, usersOff userList) string {
	userInfo := make([]string, len(usersOn))
	var hdr string
	var str string
	count := 0

	for _, user := range usersOn {
		howLong, _ := getHowLong(user.since)
		userInfo[count] = fmt.Sprintf("%s on for %s", user.name, howLong)
		count++
	}

	switch {
	case count < 1:
		var lastUser mcuser
		for _, user := range usersOff {
			if user.since.After(lastUser.since) {
				lastUser = user
			}
		}
		howLong, _ := getHowLong(lastUser.since)
		if lastUser.since.IsZero() {
			lastUser.name = "nobody"
			howLong = "ever"
		}

		hdr = fmt.Sprintf("No players for %s (%s)", howLong, lastUser.name)
	case count == 1:
		hdr = "1 player: "
	default:
		hdr = fmt.Sprintf("%d players: ", count)
	}

	str = hdr + strings.Join(userInfo, ", ")
	return str
}

//
// Starts an http server to hand out our rss data
//
func startRssServer() {
	http.HandleFunc("/mcwhorss", rssServer)
	err := http.ListenAndServe(":9092", nil)
	if err != nil {
		fmt.Println("failed to start rss server")
		return
	}
}

//
// Serve our rss data. The heading will contain a count of the number of users
// on, and the details will include who's on, and for how long. User names
// will be escaped by the html/template package.
//
func rssServer(w http.ResponseWriter, req *http.Request) {
	const xmlHdr = `<?xml version="1.0" encoding="utf-8" ?>
`
	const templateStr = `<rss version="2.0">
<channel>
  <title>Minecraft Players</title>
  <description>A list of who&#039;s logged into Minecraft, and for how long.</description>
  <item>
    <title>{{.}}</title>
    <description><p>No description</p></description>
    <guid>09as0dfu90asj</guid>
  </item>
</channel>
</rss>
`
	t, _ := template.New("feed").Parse(templateStr)
	display := getDisplay(usersOn, usersOff)
	fmt.Printf("RSS responds %s\n", display)
	io.WriteString(w, xmlHdr)
	err := t.ExecuteTemplate(w, "feed", display)
	if err != nil {
		log.Fatal(err)
	}
}

// Mcwho is a goroutine that parses and then watches a minecraft server.log file
// to determine who is connected.
func Mcwho(logPath string, conch chan mcuser, disch chan mcuser, errch chan error) {
	// Close the channel on exit so the program terminates.
	defer close(conch)
	watcher, err := setupWatcher(path.Join(logPath, "latest.log"))
	if err != nil {
		errch <- err
		return
	}
	defer watcher.Close()

	// Get a list of all the log files in the directory. We'll read them all once
	// and then watch the latest one for changes.
	files, err := ioutil.ReadDir(logPath)
	if err != nil {
		errch <- err
		return
	}
	for i := range files {
		file := files[i]
		if path.Ext(file.Name()) != gzipext {
			// only look at .gz files here
			continue
		}
		logFile := path.Join(logPath, file.Name())
		fmt.Println("reading log", logFile)
		err := readLog(logFile, conch, disch)
		if err != nil {
			fmt.Printf("Error reading log file %s: %s\n", logFile, err)
			errch <- err
			return
		}
	}

	logFile := path.Join(logPath, "latest.log")

	for {
		err := readLog(logFile, conch, disch)
		if err != nil {
			errch <- err
			return
		}

		select {
		case /*ev :=*/ <-watcher.Event:
			// naught to do but loop again
		case err := <-watcher.Error:
			errch <- err
			break
		}
	}
}

//
// Setup our fsnotify thingy so we know when the logfile gets updated.
//
func setupWatcher(logPath string) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		err = watcher.Watch(logPath)
	}

	if err != nil {
		log.Fatal(err)
	}

	return watcher, nil
}

var pos int64 // Keep track of how far we've read.
const gzipext = ".gz"

func getLogReader(logfile string, logf *os.File) (*bufio.Reader, error) {
	var rdr *bufio.Reader
	// Is this a gzip file?
	if path.Ext(logfile) == gzipext {
		gzrdr, err := gzip.NewReader(logf)
		if err != nil {
			return nil, err
		}
		rdr = bufio.NewReader(gzrdr)
	} else {
		// See if the file has shrunk. If so, read from the beginning.
		fi, err := logf.Stat()
		if err != nil {
			return nil, err
		}
		if fi.Size() < pos {
			pos = 0
		}
		logf.Seek(pos, os.SEEK_SET)
		fmt.Printf("Starting read at offset %d\n", pos)
		rdr = bufio.NewReader(logf)
	}

	return rdr, nil
}

//
// Read the log file, figure out who's on, and return a slice of users, like delicious pie.
//
var datere, logonre, logoutre *regexp.Regexp

func readLog(logfile string, conch chan mcuser, disch chan mcuser) (err error) {
	// open the log file and jump to our current location, then we'll scan it
	// one line at a time.
	logf, err := os.Open(logfile)
	if err != nil {
		return err
	}
	defer logf.Close()

	// Is this is gzip file?
	rdr, err := getLogReader(logfile, logf)
	if err != nil {
		return err
	}

	// The first time around, compile the regular expressions.
	if logonre == nil {
		datere = regexp.MustCompile(`(\d+-\d+-\d+)`)
		logonre = regexp.MustCompile(`^\[([0-9:]+)\] \[.*\]: (\S+)\[.*\] logged in with entity id`)
		logoutre = regexp.MustCompile(`^\[([0-9:]+)\] \[.*\]: (\S+) lost connection:`)
	}

	// Parse the date from the log file name. This will fail for latest.log, for that
	// use today's date
	date := time.Now().Format("2006-01-02")
	matches := datere.FindStringSubmatch(logfile)
	if matches != nil {
		date = matches[1]
	} else {
		var info os.FileInfo
		info, err = os.Stat(logfile)
		if err == nil {
			date = info.ModTime().Format("2006-01-02")
		}
	}

	for err == nil {
		var line string
		line, err = rdr.ReadString('\n')
		if matches := logonre.FindStringSubmatch(line); matches != nil {
			// log.Printf("User %s logged in at %s %s\n", matches[2], date, matches[1])
			since, _ := parseSince(date + " " + matches[1])
			conch <- mcuser{matches[2], since}
		} else if matches := logoutre.FindStringSubmatch(line); matches != nil {
			// log.Printf("User %s logged out at %s %s\n", matches[2], date, matches[1])
			since, _ := parseSince(date + " " + matches[1])
			disch <- mcuser{matches[2], since}
		}
	}

	err = nil

	// where are we?
	if path.Ext(logfile) != gzipext {
		pos, err = logf.Seek(0, os.SEEK_CUR)
	}

	return err
}

//
// Parse the time string from a Minecraft logfile into a Time value.
//
func parseSince(since string) (time.Time, error) {
	// We need to add the local time zone to the string we're parsing, or else
	// the parser will assume it's UTC.
	zone, _ := time.Now().Zone()
	since = fmt.Sprintf("%s %s", since, zone)
	ts, err := time.Parse("2006-01-02 15:04:05 MST", since)

	return ts, err
}

//
// The logfiles tell us when someone logged in. From that, figure out how long
// they've been on and return that information in the form of a string like
// "1h 34m", or "34s" if the user has been on for less than a minute.
//
func getHowLong(ts time.Time) (string, error) {

	dur := time.Now().Sub(ts)

	days := int(dur.Hours()) / 24
	hours := int(dur.Hours()) % 24
	mins := int(dur.Minutes()) % 60
	secs := int(dur.Seconds()) % 60

	str := ""
	if days > 0 {
		str += fmt.Sprintf("%dd ", days)
	}
	if hours > 0 {
		str += fmt.Sprintf("%dh ", hours)
	}
	if mins > 0 {
		str += fmt.Sprintf("%dm", mins)
	}
	if len(str) == 0 {
		str += fmt.Sprintf("%ds", secs)
	}

	// Return a string representing how long this user has been on.
	return str, nil
}
