// Command tick is the Lambda entry point. EventBridge Scheduler invokes it at
// 09:00 and 21:00; it decides for itself whether anything is due, for every
// league in LEAGUES.
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

	fleet, err := build(context.Background(), log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}

	lambda.Start(func(ctx context.Context) (app.FleetResult, error) {
		res, err := fleet.Tick(ctx, time.Now().UTC())
		if err != nil {
			log.Error("tick failed", "err", err)
		}
		return res, err
	})
}

func build(ctx context.Context, log *slog.Logger) (*app.Fleet, error) {
	cfg, err := appcfg.Load()
	if err != nil {
		return nil, err
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	client := fpl.New(fpl.WithLogger(log))
	db := dynamodb.NewFromConfig(awsCfg)
	ssmClient := ssm.NewFromConfig(awsCfg)

	// A league that cannot be built fails the whole cold start rather than
	// being dropped. A misconfigured league that silently never sends is
	// indistinguishable from a quiet week, which is the failure this stack is
	// least able to notice.
	apps := make([]*app.App, 0, len(cfg.Leagues))
	for _, league := range cfg.Leagues {
		sender, err := buildSender(ctx, cfg, league, ssmClient)
		if err != nil {
			return nil, fmt.Errorf("league %d: %w", league.ID, err)
		}
		log.Info("delivery configured",
			"league", league.ID, "channel", league.Channel, "dryRun", cfg.DryRun)

		st := store.New(db, cfg.TableName, league.ID)
		apps = append(apps, app.New(cfg, league, client, st, sender, log))
	}

	return app.NewFleet(client, apps, log), nil
}

// buildSender is the only place that knows which transports exist. Everything
// downstream sees notify.Sender. The channel is per league, so one can move to
// another transport while the rest stay put — and two can name the same
// parameter to share a webhook.
func buildSender(
	ctx context.Context, cfg *appcfg.Config, league appcfg.League, ssmClient *ssm.Client,
) (notify.Sender, error) {
	if cfg.DryRun {
		return notify.Log{}, nil
	}

	switch league.Channel {
	case appcfg.ChannelDiscord:
		webhookURL, err := secureParam(ctx, ssmClient, league.WebhookParam)
		if err != nil {
			return nil, err
		}
		return notify.NewDiscord(webhookURL), nil

	case appcfg.ChannelSlack:
		webhookURL, err := secureParam(ctx, ssmClient, league.WebhookParam)
		if err != nil {
			return nil, err
		}
		return notify.NewSlack(webhookURL), nil

	case appcfg.ChannelLog:
		return notify.Log{}, nil

	default:
		return nil, fmt.Errorf("no sender for channel %q", league.Channel)
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
