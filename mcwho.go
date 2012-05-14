//
// Copyright 2012 Geoff Hickey <trapgate@gmail.com>
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
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"flag"
	"strings"
	"regexp"
	"time"
	"log"
	"html/template"
	"github.com/howeyc/fsnotify"
)

type mcuser struct {
	name  string // user name
	since string // time logged on
}

type userList map[string]*mcuser

var	users = make(userList)

// Command-line flags
var logpath = flag.String("log-path", ".", "the location of the Minecraft server.log file")

func main() {
	flag.Parse()

	// Channels to communicate with the goroutine that watches the minecraft logfile:
	conch := make(chan mcuser)
	disch := make(chan mcuser)
	errch := make(chan error)

	// Start up our RSS server
	go startRssServer()

	// Now start up the logfile watcher
	go Mcwho(path.Join(*logpath,"server.log"), conch, disch, errch)
	for {
		select {
		case user := <-conch:
			users[user.name] = &user
			howLong, _ := getHowLong(user.since)
			log.Printf("%s on for %s\n", user.name, howLong)
		case user := <-disch:
			delete(users, user.name)
			log.Printf("%s disconnected %s\n", user.name, user.since)
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
func (p *userList) String() string {
	userInfo := make([]string, len(*p))
	hdr   := "No players"
	str   := ""
	count := 0

	for _, user := range *p {
		howLong, _ := getHowLong(user.since)
		userInfo[count] = fmt.Sprintf("%s on for %s", user.name, howLong)
		count++
	}

	switch {
	case count < 1:
		hdr = "No players"
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
	const xmlHdr = 
`<?xml version="1.0" encoding="utf-8" ?>
`
	const templateStr = 
`<rss version="2.0">
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
	t, err := template.New("feed").Parse(templateStr)
	display := users.String()
	fmt.Printf("RSS responds %s\n", display)
	io.WriteString(w, xmlHdr)
	err = t.ExecuteTemplate(w, "feed", display)
	if err != nil {
		log.Fatal(err)
	}
}

//
// Mcwho: A goroutine that parses and then watches a minecraft server.log file to determine
// who is connected.
//
func Mcwho(logPath string, conch chan mcuser, disch chan mcuser, errch chan error) {
	// Close the channel on exit so the program terminates.
	defer close(conch)
	watcher, err := setupWatcher(logPath)
	if err != nil {
		errch <- err
		return
	}
	defer watcher.Close()

	oldUsersOn := make(map[string]mcuser)
	for {
		usersOn, err := readLog(logPath)
		if err != nil {
			errch <- err
			return
		}
		for _, user := range usersOn {
			conch <- user
			oldUsersOn[user.name] = user
		}
		for _, user := range oldUsersOn {
			// if a user is in the old map but not the new one, they've disconnected
			if _, ok := usersOn[user.name]; ok == false {
				// TODO: Update the timestamp to now, so we can tell when they disconnected
				disch <- user
				delete(oldUsersOn, user.name)
			}
		}

		select {
		case /*ev :=*/ <-watcher.Event:
			//log.Println("Event!", ev)
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

//
// Read the log file, figure out who's on, and return a slice of users, like delicious pie.
//
var logonre, logoutre *regexp.Regexp
var pos int64					// Keep track of how far we've read.
func readLog(logPath string) (usersOn map[string]mcuser, err error) {
	// open the log file and jump to our current location, then we'll scan it
	// one line at a time.
	logf, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	// See if the file has shrunk. If so, read from the beginning.
	fi, err := logf.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() < pos {
		pos = 0
	}
	//logf.Seek(pos, os.SEEK_SET)
	rs  := io.ReadSeeker(logf)
	rs.Seek(pos, os.SEEK_SET)
	rdr := bufio.NewReader(rs)

	// The first time around, compile the regular expressions.
	if logonre == nil {
		logonre = regexp.MustCompile(`^([0-9\-]+ [0-9:]+) \[.*\] ([^ ]+) \[.*\] logged in`)
		logoutre = regexp.MustCompile(`^([0-9\-]+ [0-9:]+) \[.*\] ([^ ]+) lost connection`)
	}

	usersOn = make(map[string]mcuser)
	for err == nil {
		line := ""
		line, err = rdr.ReadString('\n')
		if matches := logonre.FindStringSubmatch(line); matches != nil {
			//log.Printf("User %s logged in at %s\n", matches[2], matches[1])
			usersOn[matches[2]] = mcuser{matches[2], matches[1]}
		} else if matches := logoutre.FindStringSubmatch(line); matches != nil {
			//log.Printf("User %s logged out at %s\n", matches[2], matches[1])
			delete(usersOn, matches[2])
		}
	}

	// where are we?
	pos, err = rs.Seek(0, os.SEEK_CUR)

	return usersOn, err
}

//
// The logfiles tell us when someone logged in. From that, figure out how long
// they've been on and return that information in the form of a string like
// "1h 34m", or "34s" if the user has been on for less than a minute.
//
func getHowLong(since string) (string, error) {
	// We need to add the local time zone to the string we're parsing, or else
	// the parser will assume it's UTC.
	zone, _ := time.Now().Zone()
	since = fmt.Sprintf("%s %s", since, zone)
	ts, err := time.Parse("2006-01-02 15:04:05 MST", since)
	if err != nil {
		return "???", err
	}

	dur := time.Now().Sub(ts)

	hours := int(dur.Hours())
	mins  := int(dur.Minutes()) % 60
	secs  := int(dur.Seconds()) % 60

	str := ""
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
