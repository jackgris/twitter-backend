package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/jackgris/twitter-backend/auth/internal/handler"
	"github.com/jackgris/twitter-backend/auth/internal/store/userdb"
	"github.com/jackgris/twitter-backend/auth/pkg/database"
	"github.com/jackgris/twitter-backend/auth/pkg/logger"
	"github.com/jackgris/twitter-backend/auth/pkg/msgbroker"
)

func main() {
	ctx := context.Background()
	log := logger.New(os.Stdout)
	serviceName := "auth service"
	err := run(ctx, serviceName, log)
	if err != nil {
		log.Error(ctx, serviceName+fmt.Sprintf(" Error server shutdown: %s\n", err))
	}
}

func run(ctx context.Context, serviceName string, log *logger.Logger) error {

	db := database.ConnectDB(ctx, log)
	defer db.Close(ctx)

	store := userdb.NewStore(db)

	msgBrokerPath := os.Getenv("NATS_URL")
	if msgBrokerPath == "" {
		log.Error(ctx, serviceName, "status", "Environment variable NATS_URL is empty")
		os.Exit(1)
	}
	msgbroker := msgbroker.NewMsgBroker(serviceName, msgBrokerPath, log)

	mux, u := handler.NewHandler(store, msgbroker, log)

	portEnv := os.Getenv("PORT")
	port, err := strconv.Atoi(portEnv)
	if err != nil {
		log.Error(ctx, serviceName, "status", "Environment variable PORT converting to integer")
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:    ":" + strconv.Itoa(port),
		Handler: mux,
	}

	serverErrors := make(chan error, 1)
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info(ctx, serviceName+" startup", "GOMAXPROCS", runtime.GOMAXPROCS(0), "Server started at port", port)

		serverErrors <- srv.ListenAndServe()
	}()

	go func() {
		u.SubscribeGetFollowers()
	}()

	// -------------------------------------------------------------------------
	// Shutdown

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case sig := <-shutdown:
		log.Info(ctx, serviceName+"shutdown", "status", "shutdown started", "signal", sig)
		defer log.Info(ctx, serviceName+"shutdown", "status", "shutdown complete", "signal", sig)

		msgbroker.Close()

		ctx, cancel := context.WithTimeout(ctx, time.Microsecond*500)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			return fmt.Errorf("could not stop server gracefully: %w", err)
		}
	}

	return nil
}
