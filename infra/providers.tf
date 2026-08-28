terraform {
  required_version = ">= 1.9"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.6"
    }
  }

  # Local state on purpose: single-operator side project, nothing to coordinate.
  # Swap for an S3 backend if a second machine ever needs to apply.
}

provider "aws" {
  region = var.region

  default_tags {
    tags = {
      project   = "fpl-league-bot"
      managedBy = "opentofu"
    }
  }
}
