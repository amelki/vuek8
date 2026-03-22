#!/usr/bin/env bash
#
# AWS infrastructure setup for Vue.k8 distribution
# Profile: vuek8 | Region: eu-west-3 | Account: 903895255078
#
# Usage: ./infra/setup.sh
#
set -euo pipefail

PROFILE="vuek8"
REGION="eu-west-3"
BUCKET="vuek8-releases"
TABLE="vuek8-installs"
LAMBDA_ROLE="vuek8-lambda-role"
API_NAME="vuek8-api"
ACCOUNT_ID="903895255078"

AWS="aws --profile $PROFILE --region $REGION"

echo "=== Step 1: S3 bucket ==="
if $AWS s3api head-bucket --bucket "$BUCKET" 2>/dev/null; then
    echo "Bucket $BUCKET already exists."
else
    $AWS s3api create-bucket \
        --bucket "$BUCKET" \
        --create-bucket-configuration LocationConstraint="$REGION"
    echo "Created bucket $BUCKET."
fi

# Enable static website hosting
$AWS s3 website "s3://$BUCKET/" --index-document index.html

# Bucket policy: allow CloudFront to read
cat > /tmp/vuek8-bucket-policy.json <<POLICY
{
    "Version": "2012-10-17",
    "Statement": [{
        "Sid": "AllowCloudFrontRead",
        "Effect": "Allow",
        "Principal": "*",
        "Action": "s3:GetObject",
        "Resource": "arn:aws:s3:::${BUCKET}/*"
    }]
}
POLICY
$AWS s3api put-bucket-policy --bucket "$BUCKET" --policy file:///tmp/vuek8-bucket-policy.json
echo "Bucket policy set."

echo ""
echo "=== Step 2: CloudFront distribution ==="

# Check for existing distribution
EXISTING_DIST=$($AWS cloudfront list-distributions --query "DistributionList.Items[?Origins.Items[0].DomainName=='${BUCKET}.s3.${REGION}.amazonaws.com'].Id" --output text 2>/dev/null || echo "")

if [ -n "$EXISTING_DIST" ] && [ "$EXISTING_DIST" != "None" ]; then
    echo "CloudFront distribution already exists: $EXISTING_DIST"
    CF_DOMAIN=$($AWS cloudfront get-distribution --id "$EXISTING_DIST" --query "Distribution.DomainName" --output text)
else
    cat > /tmp/vuek8-cf-config.json <<CFCONFIG
{
    "CallerReference": "vuek8-$(date +%s)",
    "Origins": {
        "Quantity": 1,
        "Items": [{
            "Id": "S3-${BUCKET}",
            "DomainName": "${BUCKET}.s3-website.${REGION}.amazonaws.com",
            "CustomOriginConfig": {
                "HTTPPort": 80,
                "HTTPSPort": 443,
                "OriginProtocolPolicy": "http-only"
            }
        }]
    },
    "DefaultCacheBehavior": {
        "TargetOriginId": "S3-${BUCKET}",
        "ViewerProtocolPolicy": "redirect-to-https",
        "AllowedMethods": {
            "Quantity": 2,
            "Items": ["GET", "HEAD"],
            "CachedMethods": { "Quantity": 2, "Items": ["GET", "HEAD"] }
        },
        "ForwardedValues": {
            "QueryString": false,
            "Cookies": { "Forward": "none" }
        },
        "MinTTL": 0,
        "DefaultTTL": 86400,
        "MaxTTL": 31536000
    },
    "Comment": "Vue.k8 releases and landing page",
    "Enabled": true,
    "DefaultRootObject": "index.html",
    "PriceClass": "PriceClass_100"
}
CFCONFIG

    CF_RESULT=$($AWS cloudfront create-distribution --distribution-config file:///tmp/vuek8-cf-config.json)
    CF_ID=$(echo "$CF_RESULT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Distribution']['Id'])")
    CF_DOMAIN=$(echo "$CF_RESULT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Distribution']['DomainName'])")
    echo "Created CloudFront distribution: $CF_ID"
fi
echo "CloudFront domain: $CF_DOMAIN"

echo ""
echo "=== Step 3: DynamoDB table ==="
if $AWS dynamodb describe-table --table-name "$TABLE" >/dev/null 2>&1; then
    echo "Table $TABLE already exists."
else
    $AWS dynamodb create-table \
        --table-name "$TABLE" \
        --attribute-definitions \
            AttributeName=installId,AttributeType=S \
            AttributeName=timestamp,AttributeType=S \
        --key-schema \
            AttributeName=installId,KeyType=HASH \
            AttributeName=timestamp,KeyType=RANGE \
        --billing-mode PAY_PER_REQUEST
    echo "Created table $TABLE."
fi

echo ""
echo "=== Step 4: Lambda IAM role ==="
if $AWS iam get-role --role-name "$LAMBDA_ROLE" >/dev/null 2>&1; then
    echo "Role $LAMBDA_ROLE already exists."
else
    cat > /tmp/vuek8-trust-policy.json <<TRUST
{
    "Version": "2012-10-17",
    "Statement": [{
        "Effect": "Allow",
        "Principal": { "Service": "lambda.amazonaws.com" },
        "Action": "sts:AssumeRole"
    }]
}
TRUST
    $AWS iam create-role \
        --role-name "$LAMBDA_ROLE" \
        --assume-role-policy-document file:///tmp/vuek8-trust-policy.json
    echo "Created role $LAMBDA_ROLE."
fi

# Attach policies
$AWS iam attach-role-policy --role-name "$LAMBDA_ROLE" \
    --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole 2>/dev/null || true
$AWS iam attach-role-policy --role-name "$LAMBDA_ROLE" \
    --policy-arn arn:aws:iam::aws:policy/AmazonDynamoDBFullAccess 2>/dev/null || true
$AWS iam attach-role-policy --role-name "$LAMBDA_ROLE" \
    --policy-arn arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess 2>/dev/null || true
echo "Policies attached."

echo ""
echo "=== Step 5: Lambda functions ==="

# --- GET /api/latest ---
mkdir -p /tmp/vuek8-lambda-latest
cat > /tmp/vuek8-lambda-latest/index.mjs <<'LATEST_FUNC'
import { S3Client, GetObjectCommand } from "@aws-sdk/client-s3";

const s3 = new S3Client({});

export const handler = async () => {
    try {
        const resp = await s3.send(new GetObjectCommand({
            Bucket: process.env.BUCKET,
            Key: "latest.json"
        }));
        const body = await resp.Body.transformToString();
        return {
            statusCode: 200,
            headers: {
                "Content-Type": "application/json",
                "Access-Control-Allow-Origin": "*"
            },
            body
        };
    } catch (err) {
        return { statusCode: 500, body: JSON.stringify({ error: err.message }) };
    }
};
LATEST_FUNC

(cd /tmp/vuek8-lambda-latest && zip -j /tmp/vuek8-lambda-latest.zip index.mjs)

# --- POST /api/ping ---
mkdir -p /tmp/vuek8-lambda-ping
cat > /tmp/vuek8-lambda-ping/index.mjs <<'PING_FUNC'
import { DynamoDBClient, PutItemCommand } from "@aws-sdk/client-dynamodb";

const dynamo = new DynamoDBClient({});

export const handler = async (event) => {
    try {
        const body = JSON.parse(event.body || "{}");
        if (!body.installId) {
            return { statusCode: 400, body: '{"error":"missing installId"}' };
        }
        await dynamo.send(new PutItemCommand({
            TableName: process.env.TABLE,
            Item: {
                installId: { S: body.installId },
                timestamp: { S: new Date().toISOString() },
                version:   { S: body.version || "unknown" },
                os:        { S: body.os || "unknown" },
                arch:      { S: body.arch || "unknown" }
            }
        }));
        return {
            statusCode: 200,
            headers: {
                "Content-Type": "application/json",
                "Access-Control-Allow-Origin": "*"
            },
            body: '{"ok":true}'
        };
    } catch (err) {
        return { statusCode: 500, body: JSON.stringify({ error: err.message }) };
    }
};
PING_FUNC

(cd /tmp/vuek8-lambda-ping && zip -j /tmp/vuek8-lambda-ping.zip index.mjs)

# Wait for IAM role propagation
echo "Waiting for IAM role propagation..."
sleep 10

ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${LAMBDA_ROLE}"

# Create or update latest function
if $AWS lambda get-function --function-name vuek8-latest >/dev/null 2>&1; then
    $AWS lambda update-function-code --function-name vuek8-latest \
        --zip-file fileb:///tmp/vuek8-lambda-latest.zip >/dev/null
    echo "Updated Lambda: vuek8-latest"
else
    $AWS lambda create-function \
        --function-name vuek8-latest \
        --runtime nodejs20.x \
        --handler index.handler \
        --role "$ROLE_ARN" \
        --zip-file fileb:///tmp/vuek8-lambda-latest.zip \
        --environment "Variables={BUCKET=$BUCKET}" \
        --timeout 10 >/dev/null
    echo "Created Lambda: vuek8-latest"
fi

# Create or update ping function
if $AWS lambda get-function --function-name vuek8-ping >/dev/null 2>&1; then
    $AWS lambda update-function-code --function-name vuek8-ping \
        --zip-file fileb:///tmp/vuek8-lambda-ping.zip >/dev/null
    echo "Updated Lambda: vuek8-ping"
else
    $AWS lambda create-function \
        --function-name vuek8-ping \
        --runtime nodejs20.x \
        --handler index.handler \
        --role "$ROLE_ARN" \
        --zip-file fileb:///tmp/vuek8-lambda-ping.zip \
        --environment "Variables={TABLE=$TABLE}" \
        --timeout 10 >/dev/null
    echo "Created Lambda: vuek8-ping"
fi

echo ""
echo "=== Step 6: API Gateway ==="

# Check for existing API
EXISTING_API=$($AWS apigatewayv2 get-apis --query "Items[?Name=='$API_NAME'].ApiId" --output text 2>/dev/null || echo "")

if [ -n "$EXISTING_API" ] && [ "$EXISTING_API" != "None" ]; then
    API_ID="$EXISTING_API"
    echo "API Gateway already exists: $API_ID"
else
    API_RESULT=$($AWS apigatewayv2 create-api \
        --name "$API_NAME" \
        --protocol-type HTTP \
        --cors-configuration AllowOrigins="*",AllowMethods="GET,POST,OPTIONS",AllowHeaders="Content-Type")
    API_ID=$(echo "$API_RESULT" | python3 -c "import sys,json; print(json.load(sys.stdin)['ApiId'])")
    echo "Created API Gateway: $API_ID"
fi

API_ENDPOINT=$($AWS apigatewayv2 get-api --api-id "$API_ID" --query "ApiEndpoint" --output text)

# Integrations
for FUNC_NAME in vuek8-latest vuek8-ping; do
    FUNC_ARN=$($AWS lambda get-function --function-name "$FUNC_NAME" --query "Configuration.FunctionArn" --output text)

    INTEGRATION_ID=$($AWS apigatewayv2 create-integration \
        --api-id "$API_ID" \
        --integration-type AWS_PROXY \
        --integration-uri "$FUNC_ARN" \
        --payload-format-version "2.0" \
        --query "IntegrationId" --output text)

    if [ "$FUNC_NAME" = "vuek8-latest" ]; then
        ROUTE_KEY="GET /api/latest"
    else
        ROUTE_KEY="POST /api/ping"
    fi

    $AWS apigatewayv2 create-route \
        --api-id "$API_ID" \
        --route-key "$ROUTE_KEY" \
        --target "integrations/$INTEGRATION_ID" >/dev/null 2>&1 || true

    # Grant API Gateway permission to invoke Lambda
    $AWS lambda add-permission \
        --function-name "$FUNC_NAME" \
        --statement-id "apigateway-$(date +%s)" \
        --action lambda:InvokeFunction \
        --principal apigateway.amazonaws.com \
        --source-arn "arn:aws:execute-api:${REGION}:${ACCOUNT_ID}:${API_ID}/*" 2>/dev/null || true
done

# Create default stage with auto-deploy
$AWS apigatewayv2 create-stage \
    --api-id "$API_ID" \
    --stage-name '$default' \
    --auto-deploy 2>/dev/null || true

echo ""
echo "=== Done ==="
echo ""
echo "CloudFront:  https://$CF_DOMAIN"
echo "API Gateway: $API_ENDPOINT"
echo ""
echo "Next steps:"
echo "  1. Update BaseURL in internal/update/check.go with the CloudFront domain"
echo "  2. Run 'make release VERSION=0.3.0' to build and upload"
echo "  3. For custom domain (vuek8.app):"
echo "     - Buy domain from registrar"
echo "     - Create Route 53 hosted zone"
echo "     - Point registrar nameservers to Route 53"
echo "     - Request ACM certificate (us-east-1 for CloudFront)"
echo "     - Add alternate domain name to CloudFront distribution"
