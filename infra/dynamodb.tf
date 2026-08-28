# Single table, single partition per league.
#
#   pk = LEAGUE#<id>
#   sk = SENT#GW#<nn>#DIGEST      one row per message actually sent (idempotency)
#        SENT#GW#<nn>#REMINDER
#        SNAPSHOT#GW#<nn>         the table as it stood, for movement baselines
#
# Gameweeks are zero-padded so a range query on sk sorts GW9 before GW10.
# No TTL: a full season is ~80 rows of a few KB, and an expiring idempotency
# marker would let an old message send again.
resource "aws_dynamodb_table" "state" {
  name         = "${var.name_prefix}-state"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pk"
  range_key    = "sk"

  attribute {
    name = "pk"
    type = "S"
  }

  attribute {
    name = "sk"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  server_side_encryption {
    enabled = true
  }
}
