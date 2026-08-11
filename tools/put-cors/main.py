import boto3
import os

s3 = boto3.client(
  's3',
  endpoint_url=os.environ['PICOTERA_S3_ENDPOINT'],
  aws_access_key_id=os.environ['PICOTERA_S3_ACCESS_KEY'],
  aws_secret_access_key=os.environ['PICOTERA_S3_SECRET_KEY'],
  region_name='garage',
)

s3.put_bucket_cors(
  Bucket=os.environ['PICOTERA_S3_BUCKET'],
  CORSConfiguration={
    'CORSRules': [
      {
        'AllowedHeaders': [
          'Authorization',
        ],
        'AllowedMethods': [
          'GET',
        ],
        'AllowedOrigins': [
          '*',
        ],
        'MaxAgeSeconds': 3000,
      },
    ],
  },
  ContentMD5='',
)
