// Command tick is the Lambda entry point. EventBridge Scheduler invokes it at
// 09:00 and 21:00; it decides for itself whether anything is due.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	// Embeds the zoneinfo database. provided.al2023 is not guaranteed to carry
	// one, and without it time.LoadLocation("Europe/London") fails at startup.
	_ "time/tzdata"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"fplbot/internal/app"
	appcfg "fplbot/internal/config"
	"fplbot/internal/fpl"
	"fplbot/internal/notify"
	"fplbot/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	application, err := build(context.Background(), log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}

	lambda.Start(func(ctx context.Context) (app.Result, error) {
		res, err := application.Tick(ctx, time.Now().UTC())
		if err != nil {
			log.Error("tick failed", "err", err)
		}
		return res, err
	})
}

func build(ctx context.Context, log *slog.Logger) (*app.App, error) {
	cfg, err := appcfg.Load()
	if err != nil {
		return nil, err
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	sender, err := buildSender(ctx, cfg, awsCfg)
	if err != nil {
		return nil, err
	}
	log.Info("delivery configured", "channel", cfg.Channel, "dryRun", cfg.DryRun)

	st := store.New(dynamodb.NewFromConfig(awsCfg), cfg.TableName, cfg.LeagueID)
	return app.New(cfg, fpl.New(), st, sender, log), nil
}

// buildSender is the only place that knows which transports exist. Everything
// downstream sees notify.Sender.
func buildSender(ctx context.Context, cfg *appcfg.Config, awsCfg aws.Config) (notify.Sender, error) {
	if cfg.DryRun {
		return notify.Log{}, nil
	}

	switch cfg.Channel {
	case appcfg.ChannelDiscord:
		webhookURL, err := secureParam(ctx, ssm.NewFromConfig(awsCfg), cfg.DiscordWebhookParam)
		if err != nil {
			return nil, err
		}
		return notify.NewDiscord(webhookURL), nil

	case appcfg.ChannelLog:
		return notify.Log{}, nil

	default:
		return nil, fmt.Errorf("no sender for channel %q", cfg.Channel)
	}
}

// secureParam reads a SecureString at cold start, so the secret is never part
// of the function's environment.
func secureParam(ctx context.Context, client *ssm.Client, name string) (string, error) {
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("reading ssm parameter %q: %w", name, err)
	}
	if out.Parameter == nil || aws.ToString(out.Parameter.Value) == "" {
		return "", fmt.Errorf("ssm parameter %q is empty", name)
	}
	return aws.ToString(out.Parameter.Value), nil
}
