package etcd

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	clientv3 "go.etcd.io/etcd/client/v3"
)

var etcd *clientv3.Client
var IsEtcd bool = false
var nodeIp string
var nodePort string
var nodeType string
var globalLogger logr.Logger

func InitEtcd(eaddr string, ipaddr string, port string, ntype string, logger logr.Logger) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{eaddr},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	logger.Info("etcd client connected", "eaddr", eaddr, "ipaddr", ipaddr, "port", port, "ntype", ntype)
	IsEtcd = true
	etcd = cli
	nodeIp = ipaddr
	nodePort = port
	nodeType = ntype
	globalLogger = logger
}

func Close() {
	if IsEtcd {
		etcd.Close()
	}
}

func RegisterSession(session string) {
	kvc := clientv3.NewKV(etcd)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	key := fmt.Sprintf("/session/%v", session)
	value := nodeIp + ":" + nodePort + ":" + nodeType
	globalLogger.Info("Regsiter Session:", "key", key, "Value", value)
	resp, _ := kvc.Put(ctx, key, value)
	rev := resp.Header.Revision
	globalLogger.Info("Register Session:", "rev", rev)
	cancel()
}
func CloseSession(session string) {
	kvc := clientv3.NewKV(etcd)
	key := fmt.Sprintf("/session/%v", session)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	resp, _ := kvc.Delete(ctx, key)
	cancel()
	globalLogger.Info("Deleted", "count", resp.Deleted)
}

func TestKV() {
	kvc := clientv3.NewKV(etcd)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	resp, err := kvc.Put(ctx, "sample_key", "sample_value")

	rev := resp.Header.Revision
	fmt.Println("Revision:", rev)

	if err != nil {
		panic(err)
	}
	fmt.Println(resp)

	gr, _ := kvc.Get(ctx, "sample_key")
	fmt.Println("Value: ", string(gr.Kvs[0].Value), "Revision: ", gr.Header.Revision)

	dresp, _ := kvc.Delete(ctx, "sample_key")
	fmt.Println("Deleted", dresp.Deleted)

	cancel()
}
