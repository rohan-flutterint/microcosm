package kvclient

import (
	"github.com/hanfei1991/microcosm/pkg/metaclient"
	"go.etcd.io/etcd/clientv3"
	etcdserverpb "go.etcd.io/etcd/etcdserver/etcdserverpb"
)

func makePutResp(etcdResp *clientv3.PutResponse) *metaclient.PutResponse {
	resp := &metaclient.PutResponse{
		Header: &metaclient.ResponseHeader{
			// [TODO] ClusterID
		},
	}
	return resp
}

func makeGetResp(etcdResp *clientv3.GetResponse) *metaclient.GetResponse {
	kvs := make([]*metaclient.KeyValue, len(etcdResp.Kvs))
	for _, kv := range etcdResp.Kvs {
		if kv.Version == 0 {
			// This key has been deleted, don't return to user
			continue
		}
		kvs = append(kvs, &metaclient.KeyValue{
			Key:   kv.Key,
			Value: kv.Value,
			// [TODO] leaseID to TTL,
			Revision:    kv.ModRevision,
		})
	}

	resp := &metaclient.GetResponse{
		Header: &metaclient.ResponseHeader{
			// [TODO] ClusterID
		},
		Kvs: kvs,
	}

	return resp
}

func makeDeleteResp(etcdResp *clientv3.DeleteResponse) *metaclient.DeleteResponse {
	resp := &metaclient.DeleteResponse{
		Header: &metaclient.ResponseHeader{
			// [TODO] ClusterID
		},
	}
	return resp
}

func makeEtcdCmpFromRev(key string, revision int64) clientv3.Cmp{
	return clientv3.Compare(clientv3.ModRevision(key), "=", revision)
}

func makeTxnResp(etcdResp *clientv3.TxnResponse) *metaclient.TxnResponse {
	rsps := make([]metaclient.ResponseOp, len(etcdResp.Responses))
	for _, eRsp := range etcdResp.Responses {
		switch eRsp.Response.(type) {
		case *etcdserverpb.ResponseOp_ResponseRange:
			rsps = append(rsps, metaclient.ResponseOp{
				Response: &metaclient.ResponseOp_ResponseGet{
					ResponseGet: makeGetResp((*clientv3.GetResponse)(eRsp.GetResponseRange())),
				},
			})
		case *etcdserverpb.ResponseOp_ResponsePut:
			rsps = append(rsps, metaclient.ResponseOp{
				Response: &metaclient.ResponseOp_ResponsePut{
					ResponsePut: makePutResp((*clientv3.PutResponse)(eRsp.GetResponsePut())),
				},
			})
		case *etcdserverpb.ResponseOp_ResponseDeleteRange:
			rsps = append(rsps, metaclient.ResponseOp{
				Response: &metaclient.ResponseOp_ResponseDelete{
					ResponseDelete: makeDeleteResp((*clientv3.DeleteResponse)(eRsp.GetResponseDeleteRange())),
				},
			})
		case *etcdserverpb.ResponseOp_ResponseTxn:
			rsps = append(rsps, metaclient.ResponseOp{
				Response: &metaclient.ResponseOp_ResponseTxn{
					ResponseTxn: makeTxnResp((*clientv3.TxnResponse)(eRsp.GetResponseTxn())),
				},
			})
		}
	}

	return &metaclient.TxnResponse{
		Header: &metaclient.ResponseHeader{
			//[ClusterID]
		},
		Responses: rsps,
	}
}

func makeNamespacePrefix(leaseID string) string {
	return leaseID + "/"
}