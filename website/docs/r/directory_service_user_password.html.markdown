---
subcategory: "Directory Service"
layout: "aws"
page_title: "AWS: aws_directory_service_user_password"
description: |-
  Sets the password of a user in an AWS Managed Microsoft AD directory, which also enables the user.
---

# Resource: aws_directory_service_user_password

Sets the password of a user in an AWS Managed Microsoft AD directory. Setting the password also enables a disabled user, so this is how a user created with [`aws_directoryservicedata_user`](directoryservicedata_user.html) becomes able to sign in.

The password is a [write-only argument](https://developer.hashicorp.com/terraform/language/resources/ephemeral#write-only-arguments): Terraform sends it to AWS but never stores it in the plan or state. Change `password_wo_version` to set a new password.

~> **NOTE:** Write-only arguments require Terraform v1.11.0 or later.

~> **NOTE:** In AWS Managed Microsoft AD, only users in the OU named after the directory's NetBIOS name can have their password set. Users in the `AWS Reserved` OU, for example, can't.

-> **NOTE:** Destroying this resource doesn't change the password or disable the user. To disable or remove the user, manage it with `aws_directoryservicedata_user`.

## Example Usage

### Password from AWS Systems Manager Parameter Store

The ephemeral parameter is read during apply and never stored.

```terraform
resource "aws_directoryservicedata_user" "example" {
  directory_id     = aws_directory_service_directory.example.id
  sam_account_name = "jane.doe"
}

ephemeral "aws_ssm_parameter" "jane_doe_password" {
  arn             = "arn:aws:ssm:us-west-2:123456789012:parameter/ad/jane.doe/password"
  with_decryption = true
}

resource "aws_directory_service_user_password" "example" {
  directory_id        = aws_directory_service_directory.example.id
  user_name           = aws_directoryservicedata_user.example.sam_account_name
  password_wo         = ephemeral.aws_ssm_parameter.jane_doe_password.value
  password_wo_version = 1
}
```

### Generated password

```terraform
ephemeral "random_password" "example" {
  length = 24
}

resource "aws_directory_service_user_password" "example" {
  directory_id        = aws_directory_service_directory.example.id
  user_name           = aws_directoryservicedata_user.example.sam_account_name
  password_wo         = ephemeral.random_password.example.result
  password_wo_version = 1
}
```

A generated password is never stored anywhere Terraform can read it back, so use this only when another process (such as a self-service password reset) gives the user their password.

## Argument Reference

The following arguments are required:

* `directory_id` - (Required) ID of the directory that contains the user.
* `password_wo` - (Required, Write-Only) New password for the user. Must be between 1 and 127 characters and meet the directory's password complexity requirements. Never stored in the plan or state.
* `password_wo_version` - (Required) Version of `password_wo`. Change this value (for example, increment it) to set the password again; changes to `password_wo` alone aren't detected because the password is never stored.
* `user_name` - (Required) SAM account name of the user.

The following arguments are optional:

* `region` - (Optional) Region where the user's password is managed. Defaults to the provider region.

## Attribute Reference

This resource exports the following attributes in addition to the arguments above:

* `user_sid` - Security identifier (SID) of the user when the password was set. Requires [Directory Service Data](https://docs.aws.amazon.com/directoryservice/latest/admin-guide/ms_ad_getting_started_directory_service_data.html) access (`enable_directory_data_access` on `aws_directory_service_directory`); empty otherwise.

When Directory Service Data access is enabled, Terraform checks on each refresh that the user still exists. If the user was deleted, or deleted and created again under the same name (a new SID, and a disabled user), Terraform plans to set the password again. Without Directory Service Data access, no such check is possible and the resource is left as it is.

## Timeouts

[Configuration options](https://developer.hashicorp.com/terraform/language/resources/syntax#operation-timeouts):

* `create` - (Default `5m`) How long to retry setting the password while a newly created user replicates between domain controllers.
* `update` - (Default `5m`) The same, when `password_wo_version` changes.

## Import

In Terraform v1.12.0 and later, the [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used with the `identity` attribute. For example:

```terraform
import {
  to = aws_directory_service_user_password.example
  identity = {
    directory_id = "d-1234567890"
    user_name    = "jane.doe"
  }
}

resource "aws_directory_service_user_password" "example" {
  directory_id        = "d-1234567890"
  user_name           = "jane.doe"
  password_wo         = ephemeral.aws_ssm_parameter.jane_doe_password.value
  password_wo_version = 1
}
```

An imported resource has no `password_wo_version` in state, so the first apply after import sets the password from configuration.

### Identity Schema

#### Required

* `directory_id` (String) ID of the directory that contains the user.
* `user_name` (String) SAM account name of the user.

#### Optional

* `account_id` (String) AWS Account where this resource is managed.
* `region` (String) Region where this resource is managed.

In Terraform v1.5.0 and later, use an [`import` block](https://developer.hashicorp.com/terraform/language/import) to import a Directory Service User Password using the directory ID and user name separated by a comma. For example:

```terraform
import {
  to = aws_directory_service_user_password.example
  id = "d-1234567890,jane.doe"
}
```

Using `terraform import`, import a Directory Service User Password using the directory ID and user name separated by a comma. For example:

```console
% terraform import aws_directory_service_user_password.example d-1234567890,jane.doe
```
