package server

import (
	"context"
	"fmt"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// NewHealthService creates a new HealthServiceServer
func NewHealthService(agent *Agent) healthpb.HealthServer {
	return &healthService{
		agent: agent,
	}
}

type healthService struct {
	health.Server
	agent *Agent
}

func (s *healthService) Check(ctx context.Context, in *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	db, err := s.agent.acc.Get(fmt.Sprintf("%s:%d", s.agent.mysqlAdminHostname, s.agent.mysqlAdminPort), moco.MiscUser, s.agent.miscUserPassword)
	if err != nil {
		log.Error("failed to connect to database before health check", map[string]interface{}{
			"hostname":  s.agent.mysqlAdminHostname,
			"port":      s.agent.mysqlAdminPort,
			log.FnError: err,
		})
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_UNKNOWN}, status.Errorf(codes.Internal, "failed to connect to database before health check: err=%v", err)
	}

	replicaStatus, err := accessor.GetMySQLReplicaStatus(ctx, db)
	if err != nil {
		log.Error("failed to get replica status", map[string]interface{}{
			"hostname":  s.agent.mysqlAdminHostname,
			"port":      s.agent.mysqlAdminPort,
			log.FnError: err,
		})
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_UNKNOWN}, status.Errorf(codes.Internal, "failed to get replica status: err=%v", err)
	}

	cloneStatus, err := accessor.GetMySQLCloneStateStatus(ctx, db)
	if err != nil {
		log.Error("failed to get clone status", map[string]interface{}{
			"hostname":  s.agent.mysqlAdminHostname,
			"port":      s.agent.mysqlAdminPort,
			log.FnError: err,
		})
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_UNKNOWN}, status.Errorf(codes.Internal, "failed to get clone status: err=%v", err)
	}

	// When the instance has been switched from replica to primary, replicaStatus can be not nil.
	// In this case, Replica_{IO|SQL}_Running becomes "No" without any errors,
	// but replicaStatus.SlaveIOState will be the empty string "".
	// The below conditions utilize this to know the own instance works as primary or replica.
	var hasIOThreadError, hasSQLThreadError, isUnderCloning bool
	if replicaStatus != nil &&
		replicaStatus.SlaveIOState != "" &&
		(replicaStatus.LastIoErrno != 0 || replicaStatus.SlaveIORunning != moco.ReplicaRunConnect) {
		hasIOThreadError = true
	}

	if replicaStatus != nil &&
		replicaStatus.SlaveIOState != "" &&
		(replicaStatus.LastSQLErrno != 0 || replicaStatus.SlaveSQLRunning != moco.ReplicaRunConnect) {
		hasSQLThreadError = true
	}

	if cloneStatus.State.Valid && cloneStatus.State.String != moco.CloneStatusCompleted {
		isUnderCloning = true
	}
	if hasIOThreadError || hasSQLThreadError || isUnderCloning {
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_NOT_SERVING}, status.Errorf(codes.Unavailable, "isOutOfSynced=%t, hasSQLThreadError=%t, isUnderCloning=%t", hasIOThreadError, hasSQLThreadError, isUnderCloning)
	}

	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}
