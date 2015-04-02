package webserver

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/samalba/dockerclient"
)

const (
	SWARM_HOST    = "http://192.168.156.200:2375"
	REGISTRY_HOST = "192.168.156.200:5000"
)

func initRedisCluster() {
	redisID, err1 := runRedis(SWARM_HOST)
	if err1 != nil {
		log.Fatal(err1)
	}

	redisInfo, err2 := inspect(SWARM_HOST, redisID)
	if err2 != nil {
		log.Fatal(err2)
	}
	redisHost := getRedisHost(redisInfo)
	proxyID, err3 := runProxy(SWARM_HOST, []string{redisHost})
	if err3 != nil {
		log.Fatal(err3)
	}
	fmt.Println(proxyID)
}

func runProxy(swarmHost string, redisHosts []string) (string, error) {
	cli, err := dockerclient.NewDockerClient(swarmHost, nil)
	if err != nil {
		return "", err
	}
	defer closeIdleConnections(cli.HTTPClient)
	portbind := make(map[string][]dockerclient.PortBinding)
	portbind["19000/tcp"] = []dockerclient.PortBinding{
		dockerclient.PortBinding{
			HostIp:   "0.0.0.0",
			HostPort: "19000",
		},
	}
	portbind["11000/tcp"] = []dockerclient.PortBinding{
		dockerclient.PortBinding{
			HostIp:   "0.0.0.0",
			HostPort: "11000",
		},
	}
	portbind["18087/tcp"] = []dockerclient.PortBinding{
		dockerclient.PortBinding{
			HostIp:   "0.0.0.0",
			HostPort: "18087",
		},
	}
	containerConfig := &dockerclient.ContainerConfig{
		Image: REGISTRY_HOST + "/proxy-etcd",
		HostConfig: dockerclient.HostConfig{
			NetworkMode:  "bridge",
			PortBindings: portbind,
		},
	}

	containerId, err := cli.CreateContainer(containerConfig, "proxy-test")
	if err != nil {
		return "", errors.New("ERR on create:" + err.Error())
	}

	// Start the container
	err = cli.StartContainer(containerId, nil)
	if err != nil {
		return "", errors.New("ERR on start:" + err.Error())
	}

	time.Sleep(1 * time.Second)
	execConfig := &dockerclient.ExecConfig{
		AttachStdin:  false,
		AttachStdout: false,
		AttachStderr: false,
		Tty:          false,
		Cmd:          []string{"/etcd/etcd"},
		Container:    containerId,
		Detach:       true,
	}
	_, _, err = cli.Exec(execConfig)
	if err != nil {
		return "", errors.New("ERR on exec1:" + err.Error())
	}

	time.Sleep(1 * time.Second)
	execConfig.Cmd = []string{"/codis/bin/codis-config", "dashboard", "--addr", "0.0.0.0:18087"}
	_, _, err = cli.Exec(execConfig)
	if err != nil {
		return "", errors.New("ERR on exec2:" + err.Error())
	}

	time.Sleep(1 * time.Second)
	execConfig.AttachStdin = true
	execConfig.AttachStdout = true
	execConfig.Cmd = []string{"/codis/bin/codis-config", "slot", "init"}
	var resp string
	resp, _, err = cli.Exec(execConfig)
	if err != nil {
		return "", errors.New("ERR on exec3:" + err.Error())
	}
	fmt.Println(resp)

	time.Sleep(1 * time.Second)
	execConfig.AttachStdin = false
	execConfig.AttachStdout = false
	execConfig.Cmd = []string{"/codis/bin/codis-config", "server", "add", "1", redisHosts[0], "master"}
	_, _, err = cli.Exec(execConfig)
	if err != nil {
		return "", errors.New("ERR on exec4:" + err.Error())
	}

	time.Sleep(5 * time.Second)
	execConfig.Cmd = []string{"/codis/bin/codis-proxy", "-c", "config.ini", "-L", "./proxy.log", "--cpu=1", "--addr=0.0.0.0:19000", "--http-addr=0.0.0.0:11000"}
	_, _, err = cli.Exec(execConfig)
	if err != nil {
		return "", errors.New("ERR on exec5:" + err.Error())
	}

	time.Sleep(2 * time.Second)
	execConfig.Cmd = []string{"/codis/bin/codis-config", "-c", "config.ini", "proxy", "online", "proxy_1"}
	_, _, err = cli.Exec(execConfig)
	if err != nil {
		return "", errors.New("ERR on exec6:" + err.Error())
	}
	return containerId, nil
}

//半成品，还需要加强错误处理，比如出现错误删除已建容器
func runRedis(swarmHost string) (string, error) {
	cli, err := dockerclient.NewDockerClient(swarmHost, nil)
	if err != nil {
		return "", err
	}
	defer closeIdleConnections(cli.HTTPClient)
	portbind := make(map[string][]dockerclient.PortBinding)
	portbind["6379/tcp"] = []dockerclient.PortBinding{
		dockerclient.PortBinding{
			HostIp:   "0.0.0.0",
			HostPort: "6379",
		},
	}
	containerConfig := &dockerclient.ContainerConfig{
		Image: REGISTRY_HOST + "/redis",
		HostConfig: dockerclient.HostConfig{
			NetworkMode:  "bridge",
			PortBindings: portbind,
		},
	}

	containerId, err := cli.CreateContainer(containerConfig, "redis-test")
	if err != nil {
		return "", errors.New("ERR on create:" + err.Error())
	}

	// Start the container
	err = cli.StartContainer(containerId, nil)
	if err != nil {
		return "", errors.New("ERR on start:" + err.Error())
	}
	execConfig := &dockerclient.ExecConfig{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          []string{"/codis/codis-server"},
		Container:    containerId,
		Detach:       true,
	}
	_, _, err = cli.Exec(execConfig)
	if err != nil {
		return "", errors.New("ERR on exec:" + err.Error())
	}
	return containerId, nil
}

func getRedisHost(info *dockerclient.ContainerInfo) string {
	hostIp := info.Node.Ip
	port := info.HostConfig.PortBindings["6379/tcp"][0].HostPort
	return hostIp + ":" + port
}

func inspect(swarmHost string, containerId string) (*dockerclient.ContainerInfo, error) {
	cli, err := dockerclient.NewDockerClient(swarmHost, nil)
	if err != nil {
		return &dockerclient.ContainerInfo{}, err
	}
	defer closeIdleConnections(cli.HTTPClient)
	return cli.InspectContainer(containerId)
}

func closeIdleConnections(client *http.Client) {
	if tr, ok := client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}
