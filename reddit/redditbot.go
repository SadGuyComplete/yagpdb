package reddit

import (
	"fmt"
	"github.com/jonas747/go-reddit"
	"github.com/jonas747/yagpdb/common"
	"github.com/jonas747/yagpdb/common/mqueue"
	"github.com/mediocregopher/radix.v2/redis"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ClientID     = os.Getenv("YAGPDB_REDDIT_CLIENTID")
	ClientSecret = os.Getenv("YAGPDB_REDDIT_CLIENTSECRET")
	RedirectURI  = os.Getenv("YAGPDB_REDDIT_REDIRECT")
	RefreshToken = os.Getenv("YAGPDB_REDDIT_REFRESHTOKEN")
)

func (p *Plugin) StartFeed() {
	go p.runBot()
}

func (p *Plugin) StopFeed(wg *sync.WaitGroup) {
	p.stopFeedChan <- wg
}

func UserAgent() string {
	return fmt.Sprintf("YAGPDB:%s:%s (by /u/jonas747)", ClientID, common.VERSIONNUMBER)
}

func setupClient() *reddit.Client {
	authenticator := reddit.NewAuthenticator(UserAgent(), ClientID, ClientSecret, RedirectURI, "a", reddit.ScopeEdit+" "+reddit.ScopeRead)
	redditClient := authenticator.GetAuthClient(&oauth2.Token{RefreshToken: RefreshToken}, UserAgent())
	return redditClient
}

func (p *Plugin) runBot() {

	redditClient := setupClient()
	redisClient := common.MustGetRedisClient()

	fetcher := NewPostFetcher(redditClient, redisClient)

	ticker := time.NewTicker(time.Second * 5)
	for {
		select {
		case wg := <-p.stopFeedChan:
			wg.Done()
			return
		case <-ticker.C:
		}

		links, err := fetcher.GetNewPosts()
		if err != nil {
			logrus.WithError(err).Error("Error fetchind new links")
			continue
		}
		if len(links) < 1 {
			continue
		}

		for _, v := range links {
			// since := time.Since(time.Unix(int64(v.CreatedUtc), 0))
			// logrus.Debugf("[%5.2fs %6s] /r/%-20s: %s", since.Seconds(), v.ID, v.Subreddit, v.Title)
			p.handlePost(v, redisClient)
		}
	}
}

func (p *Plugin) handlePost(post *reddit.Link, redisClient *redis.Client) error {

	// createdSince := time.Since(time.Unix(int64(post.CreatedUtc), 0))
	// logrus.Printf("[%5.1fs] /r/%-15s: %s, %s", createdSince.Seconds(), post.Subreddit, post.Title, post.ID)

	config, err := GetConfig(redisClient, "global_subreddit_watch:"+strings.ToLower(post.Subreddit))
	if err != nil {
		logrus.WithError(err).Error("Failed getting config from redis")
		return err
	}

	// Get the channels that listens to this subreddit, if any

	filteredItems := make([]*SubredditWatchItem, 0, len(config))

OUTER:
	for _, c := range config {
		for _, v := range filteredItems {
			if v.Channel == c.Channel {
				continue OUTER
			}
		}

		filteredItems = append(filteredItems, c)
	}

	// No channels nothing to do...
	if len(filteredItems) < 1 {
		return nil
	}

	logrus.WithFields(logrus.Fields{
		"num_channels": len(filteredItems),
		"subreddit":    post.Subreddit,
	}).Info("Found matched reddit post")

	message := CreatePostMessage(post)

	for _, item := range filteredItems {
		mqueue.QueueMessageString("reddit", item.Guild+":"+strconv.Itoa(item.ID), item.Channel, message)
	}

	return nil
}

func CreatePostMessage(post *reddit.Link) string {
	typeStr := "link"
	if post.IsSelf {
		typeStr = "self post"
	}

	body := fmt.Sprintf("<:reddit:462994034428870656> **/u/%s** posted a new %s in **/r/%s**\n**%s** - <%s>\n",
		post.Author, typeStr, post.Subreddit, post.Title, "https://redd.it/"+post.ID)

	if post.IsSelf {
		body += common.CutStringShort(post.Selftext, 250)
	} else {
		body += post.URL
	}

	return body
}

type RedditIdSlice []string

// Len is the number of elements in the collection.
func (r RedditIdSlice) Len() int {
	return len(r)
}

// Less reports whether the element with
// index i should sort before the element with index j.
func (r RedditIdSlice) Less(i, j int) bool {
	a, err1 := strconv.ParseInt(r[i], 36, 64)
	b, err2 := strconv.ParseInt(r[j], 36, 64)
	if err1 != nil {
		logrus.WithError(err1).Error("Failed parsing id")
	}
	if err2 != nil {
		logrus.WithError(err2).Error("Failed parsing id")
	}

	return a > b
}

// Swap swaps the elements with indexes i and j.
func (r RedditIdSlice) Swap(i, j int) {
	temp := r[i]
	r[i] = r[j]
	r[j] = temp
}
