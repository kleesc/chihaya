// Copyright 2013 The Chihaya Authors. All rights reserved.
// Use of this source code is governed by the BSD 2-Clause license,
// which can be found in the LICENSE file.

package redis

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/garyburd/redigo/redis"

	"github.com/chihaya/chihaya/config"
	"github.com/chihaya/chihaya/storage"
)

var (
	testTorrentIDChannel chan uint64
	testUserIDChannel    chan uint64
	testPeerIDChannel    chan int
)

func init() {
	testTorrentIDChannel = make(chan uint64, 100)
	testUserIDChannel = make(chan uint64, 100)
	testPeerIDChannel = make(chan int, 100)
	// Sync access to ID counter with buffered global channels
	go func() {
		for i := 0; ; i++ {
			testTorrentIDChannel <- uint64(i)
		}
	}()
	go func() {
		for i := 0; ; i++ {
			testUserIDChannel <- uint64(i)
		}
	}()
	go func() {
		for i := 0; ; i++ {
			testPeerIDChannel <- i
		}
	}()
}

func createTestTorrentID() uint64 {
	return <-testTorrentIDChannel
}

func createTestUserID() uint64 {
	return <-testUserIDChannel
}

func createTestPeerID() string {
	return "-testPeerID-" + strconv.Itoa(<-testPeerIDChannel)
}

func createTestInfohash() string {
	uuid := make([]byte, 40)
	n, err := io.ReadFull(rand.Reader, uuid)
	if n != len(uuid) || err != nil {
		panic(err)
	}
	return string(uuid)
}

func createTestPasskey() string {
	uuid := make([]byte, 40)
	n, err := io.ReadFull(rand.Reader, uuid)
	if n != len(uuid) || err != nil {
		panic(err)
	}
	return string(uuid)
}

func panicOnErr(err error) {
	if err != nil {
		fmt.Println(err)
		panic(err)
	}
}

func createTestRedisConn() *Conn {
	testConfig, err := config.Open(os.Getenv("TESTCONFIGPATH"))
	conf := &testConfig.Cache
	panicOnErr(err)

	testPool := &Pool{
		conf: conf,
		pool: redis.Pool{
			MaxIdle:      conf.MaxIdleConns,
			IdleTimeout:  conf.IdleTimeout.Duration,
			Dial:         makeDialFunc(conf),
			TestOnBorrow: testOnBorrow,
		},
	}

	newConn := &Conn{
		conf: testPool.conf,
		done: false,
		Conn: testPool.pool.Get(),
	}
	panicOnErr(err)

	// Test connection before returning
	_, err = newConn.Do("PING")
	panicOnErr(err)
	return newConn
}

func createTestUser() *storage.User {
	return &storage.User{ID: createTestUserID(), Passkey: createTestPasskey(),
		UpMultiplier: 1.01, DownMultiplier: 1.0, Slots: 4, SlotsUsed: 2, Snatches: 7}
}

func createTestPeer(userID uint64, torrentID uint64) *storage.Peer {

	return &storage.Peer{ID: createTestPeerID(), UserID: userID, TorrentID: torrentID,
		IP: "127.0.0.1", Port: 6889, Uploaded: 1024, Downloaded: 3000, Left: 4200, LastAnnounce: 11}
}

func createTestPeers(torrentID uint64, num int) map[string]storage.Peer {
	testPeers := make(map[string]storage.Peer)
	for i := 0; i < num; i++ {
		tempPeer := createTestPeer(createTestUserID(), torrentID)
		testPeers[storage.PeerMapKey(tempPeer)] = *tempPeer
	}
	return testPeers
}

func createTestTorrent() *storage.Torrent {

	torrentInfohash := createTestInfohash()
	torrentID := createTestTorrentID()

	testSeeders := createTestPeers(torrentID, 4)
	testLeechers := createTestPeers(torrentID, 2)

	testTorrent := storage.Torrent{ID: torrentID, Infohash: torrentInfohash, Active: true,
		Seeders: testSeeders, Leechers: testLeechers, Snatches: 11, UpMultiplier: 1.0, DownMultiplier: 1.0, LastAction: 0}
	return &testTorrent
}

func TestValidPeers(t *testing.T) {
	testConn := createTestRedisConn()
	testTorrentID := createTestTorrentID()
	testPeers := createTestPeers(testTorrentID, 3)

	panicOnErr(testConn.addPeers(testPeers, "test:"))
	peerMap, err := testConn.getPeers(testTorrentID, "test:")
	panicOnErr(err)
	if len(peerMap) != len(testPeers) {
		t.Error("Num Peers not equal ", len(peerMap), len(testPeers))
	}
	panicOnErr(testConn.removePeers(testTorrentID, testPeers, "test:"))
}

func TestInvalidPeers(t *testing.T) {
	testConn := createTestRedisConn()
	testTorrentID := createTestTorrentID()
	testPeers := createTestPeers(testTorrentID, 3)
	tempPeer := createTestPeer(createTestUserID(), testTorrentID)
	testPeers[storage.PeerMapKey(tempPeer)] = *tempPeer

	panicOnErr(testConn.addPeers(testPeers, "test:"))
	// Imitate a peer being removed during get
	hashKey := testConn.conf.Prefix + getPeerHashKey(tempPeer)
	_, err := testConn.Do("DEL", hashKey)
	panicOnErr(err)

	peerMap, err := testConn.getPeers(testTorrentID, "test:")
	panicOnErr(err)
	// Expect 1 less peer due to delete
	if len(peerMap) != len(testPeers)-1 {
		t.Error("Num Peers not equal ", len(peerMap), len(testPeers)-1)
	}
	panicOnErr(testConn.removePeers(testTorrentID, testPeers, "test:"))
	if len(testPeers) != 0 {
		t.Errorf("All peers not removed, %d peers remain!", len(testPeers))
	}
}
