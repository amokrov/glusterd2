package e2e

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/gluster/glusterd2/pkg/api"

	"github.com/stretchr/testify/require"
)

// TestBrickMux tests brick multiplexing.
func TestBrickMux(t *testing.T) {
	var err error

	r := require.New(t)

	tc, err := setupCluster(t, "./config/1.toml")
	r.Nil(err)
	defer teardownCluster(tc)

	client, err = initRestclient(tc.gds[0])
	r.Nil(err)
	r.NotNil(client)

	// Turn on brick mux cluster option
	optReq := api.ClusterOptionReq{
		Options: map[string]string{"cluster.brick-multiplex": "on"},
	}
	err = client.ClusterOptionSet(optReq)
	r.Nil(err)

	// Create a 1 x 3 volume
	var brickPaths []string
	for i := 1; i <= 3; i++ {
		brickPath := testTempDir(t, "brick")
		brickPaths = append(brickPaths, brickPath)
	}

	volname1 := formatVolName(t.Name())

	createReq := api.VolCreateReq{
		Name: volname1,
		Subvols: []api.SubvolReq{
			{
				Type: "distribute",
				Bricks: []api.BrickReq{
					{PeerID: tc.gds[0].PeerID(), Path: brickPaths[0]},
					{PeerID: tc.gds[0].PeerID(), Path: brickPaths[1]},
					{PeerID: tc.gds[0].PeerID(), Path: brickPaths[2]},
				},
			},
		},
		Force: true,
	}
	_, err = client.VolumeCreate(createReq)
	r.Nil(err)

	// start the volume
	err = client.VolumeStart(volname1, false)
	r.Nil(err)

	// check bricks status and confirm that bricks have been multiplexed
	bstatus, err := client.BricksStatus(volname1)
	r.Nil(err)

	// NOTE: Track these variables through-out the test.
	pid := bstatus[0].Pid
	port := bstatus[0].Port

	for _, b := range bstatus {
		r.Equal(pid, b.Pid)
		r.Equal(port, b.Port)
	}

	// create another compatible volume

	for i := 4; i <= 5; i++ {
		brickPath := testTempDir(t, "brick")
		brickPaths = append(brickPaths, brickPath)
	}

	volname2 := volname1 + "2"

	createReq = api.VolCreateReq{
		Name: volname2,
		Subvols: []api.SubvolReq{
			{
				Type: "distribute",
				Bricks: []api.BrickReq{
					{PeerID: tc.gds[0].PeerID(), Path: brickPaths[3]},
					{PeerID: tc.gds[0].PeerID(), Path: brickPaths[4]},
				},
			},
		},
		Force: true,
	}
	_, err = client.VolumeCreate(createReq)
	r.Nil(err)

	err = client.VolumeStart(volname2, false)
	r.Nil(err)

	// check bricks status and confirm that bricks have been multiplexed
	// onto bricks of the first volume
	bstatus, err = client.BricksStatus(volname2)
	r.Nil(err)

	// the pid and port variables now point to values from the old original volume
	for _, b := range bstatus {
		r.Equal(pid, b.Pid)
		r.Equal(port, b.Port)
	}

	// kill the brick from first volume into which all the brick have been multiplexed
	process, err := os.FindProcess(pid)
	r.Nil(err, fmt.Sprintf("failed to find bricks pid: %s", err))
	err = process.Signal(syscall.Signal(15))
	r.Nil(err, fmt.Sprintf("failed to kill bricks: %s", err))

	time.Sleep(time.Second * 1)
	bstatus, err = client.BricksStatus(volname1)
	r.Nil(err)
	r.Equal(bstatus[0].Pid, 0)
	r.Equal(bstatus[0].Port, 0)

	// Second volume's bricks should become offline since brick from first volume has been killed
	bstatus, err = client.BricksStatus(volname2)
	r.Nil(err)
	for _, b := range bstatus {
		r.Equal(b.Online, false)
	}

	// force start the first and second volume
	err = client.VolumeStart(volname1, true)
	r.Nil(err)

	err = client.VolumeStart(volname2, true)
	r.Nil(err)

	// first brick from first volume should now all bricks of first volume
	// should be  be multiplexed into a new pid
	bstatus, err = client.BricksStatus(volname1)
	r.Nil(err)

	pid = bstatus[0].Pid
	port = bstatus[0].Port

	// force start the second volume and the bricks of second volume should
	// now be multiplexed into the pid in which bricks of first volume are multiplexed
	bstatus, err = client.BricksStatus(volname2)
	r.Nil(err)

	for _, b := range bstatus {
		r.Equal(pid, b.Pid)
		r.Equal(port, b.Port)
	}

	// stop the second volume, make it incompatible for multiplexing and start it again.
	// this should start the bricks as separate processes.
	r.Nil(client.VolumeStop(volname2))

	voloptReq := api.VolOptionReq{
		Options: map[string]string{"write-behind.trickling-writes": "on"},
	}
	voloptReq.AllowAdvanced = true
	err = client.VolumeSet(volname2, voloptReq)
	r.Nil(err)

	err = client.VolumeStart(volname2, false)
	r.Nil(err)

	bstatus, err = client.BricksStatus(volname2)
	r.Nil(err)

	// the pid and port variables point to values from the old values
	// the bricks should have different values for pid and port as they
	// are no longer multiplexed
	for _, b := range bstatus {
		r.NotEqual(pid, b.Pid)
		r.NotEqual(port, b.Port)
	}

	r.Nil(client.VolumeStop(volname2))
	r.Nil(client.VolumeStop(volname1))

	r.Nil(client.VolumeDelete(volname2))
	r.Nil(client.VolumeDelete(volname1))

	for i := 6; i <= 36; i++ {
		brickPath := testTempDir(t, "brick")
		brickPaths = append(brickPaths, brickPath)
	}

	// Create 10 volumes and start all 10
	// making all brick multiplexed into first brick of
	// first volume.
	index := 5
	for i := 1; i <= 10; i++ {
		createReq := api.VolCreateReq{
			Name: volname1 + strconv.Itoa(i),
			Subvols: []api.SubvolReq{
				{
					Type: "distribute",
					Bricks: []api.BrickReq{
						{PeerID: tc.gds[0].PeerID(), Path: brickPaths[index]},
						{PeerID: tc.gds[0].PeerID(), Path: brickPaths[index+1]},
						{PeerID: tc.gds[0].PeerID(), Path: brickPaths[index+2]},
					},
				},
			},
			Force: true,
		}
		_, err = client.VolumeCreate(createReq)
		r.Nil(err)

		// start the volume
		err = client.VolumeStart(volname1+strconv.Itoa(i), false)
		r.Nil(err)

		index = index + 3
	}

	// Check if the multiplexing was successful
	for i := 1; i <= 10; i++ {

		bstatus, err = client.BricksStatus(volname1 + strconv.Itoa(i))
		r.Nil(err)

		if i == 1 {
			pid = bstatus[0].Pid
			port = bstatus[0].Port
		} else {
			for _, b := range bstatus {
				r.Equal(pid, b.Pid)
				r.Equal(port, b.Port)
			}
		}

	}

	// Stop glusterd2 instance and kill the glusterfsd into
	// whcih all bricks were multiplexed
	r.Nil(tc.gds[0].Stop())
	process, err = os.FindProcess(pid)
	r.Nil(err, fmt.Sprintf("failed to find brick pid: %s", err))
	err = process.Signal(syscall.Signal(15))
	r.Nil(err, fmt.Sprintf("failed to kill brick: %s", err))

	// Spawn glusterd2 instance
	gd, err := spawnGlusterd(t, "./config/1.toml", false)
	r.Nil(err)
	r.True(gd.IsRunning())

	// Check if all the bricks are multiplexed into the first brick
	// of first volume, this time with a different pid and port.
	for i := 1; i <= 10; i++ {

		bstatus, err = client.BricksStatus(volname1 + strconv.Itoa(i))
		r.Nil(err)
		if i == 1 {
			pid = bstatus[0].Pid
			port = bstatus[0].Port
		}
		for _, b := range bstatus {
			r.Equal(pid, b.Pid)
			r.Equal(port, b.Port)
		}
	}

	for i := 1; i <= 10; i++ {
		r.Nil(client.VolumeStop(volname1 + strconv.Itoa(i)))
		r.Nil(client.VolumeDelete(volname1 + strconv.Itoa(i)))
	}
	r.Nil(gd.Stop())
}
