---
subcategory: "Directory Service Data"
layout: "aws"
page_title: "AWS: aws_directoryservicedata_group_member"
description: |-
  Manages a group membership in an AWS Directory Service directory.
---

# Resource: aws_directoryservicedata_group_member

Manages a group membership in an AWS Directory Service directory. Each resource adds one user, group, or computer to one group.

~> **NOTE:** Changing any argument removes the member from the group and adds the new one, because AWS has no API to update a membership.

-> **NOTE:** Directory Service Data allows 4 write requests per second per directory on Standard edition and 8 on Enterprise edition, with 10 concurrent requests. Large numbers of memberships in one configuration may be throttled; the provider retries throttled requests, and a lower `-parallelism` reduces them.

## Example Usage

### Add a user to a group

The following example assumes an existing Directory Service directory with Directory Service Data access enabled.

```terraform
resource "aws_directoryservicedata_group" "example" {
  directory_id     = "d-1234567890"
  sam_account_name = "app-admins"
}

resource "aws_directoryservicedata_user" "example" {
  directory_id     = "d-1234567890"
  sam_account_name = "jane.doe"
}

resource "aws_directoryservicedata_group_member" "example" {
  directory_id = "d-1234567890"
  group_name   = aws_directoryservicedata_group.example.sam_account_name
  member_name  = aws_directoryservicedata_user.example.sam_account_name
}
```

### Nest a group in another group

```terraform
resource "aws_directoryservicedata_group_member" "example" {
  directory_id = "d-1234567890"
  group_name   = aws_directoryservicedata_group.parent.sam_account_name
  member_name  = aws_directoryservicedata_group.child.sam_account_name
}
```

### Add a member from a trusted domain

```terraform
resource "aws_directoryservicedata_group_member" "example" {
  directory_id = "d-1234567890"
  group_name   = aws_directoryservicedata_group.example.sam_account_name
  member_name  = "jane.doe"
  member_realm = "trusted.example.com"
}
```

## Argument Reference

The following arguments are required:

* `directory_id` - (Required) ID of the directory that's associated with the group.
* `group_name` - (Required) SAM account name of the group. Must be between 1 and 64 characters and must not contain any of the following characters: `: ; | = + " * ? < > / \ , [ ] @`.
* `member_name` - (Required) SAM account name of the user, group, or computer to add to the group. Must be between 1 and 63 characters and must not contain the characters listed for `group_name`.

The following arguments are optional:

* `member_realm` - (Optional) Domain name of the member. Required only for a member from a domain outside the directory, such as a trusted domain. Defaults to the directory's own domain.
* `region` - (Optional) Region where the group membership is managed. Defaults to the provider region.

`group_name`, `member_name`, and `member_realm` are matched without regard to letter case, as Active Directory does. The values are kept as configured.

## Attribute Reference

This resource exports the following attributes in addition to the arguments above:

* `member_sid` - Security identifier (SID) of the member.
* `member_type` - Type of the member. One of `USER`, `GROUP`, or `COMPUTER`.

## Timeouts

[Configuration options](https://developer.hashicorp.com/terraform/language/resources/syntax#operation-timeouts):

* `create` - (Default `5m`) How long to retry adding the member while a newly created group or member replicates between domain controllers.

## Import

In Terraform v1.12.0 and later, the [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used with the `identity` attribute. For example:

```terraform
import {
  to = aws_directoryservicedata_group_member.example
  identity = {
    directory_id = "d-1234567890"
    group_name   = "app-admins"
    member_name  = "jane.doe"
  }
}

resource "aws_directoryservicedata_group_member" "example" {
  directory_id = "d-1234567890"
  group_name   = "app-admins"
  member_name  = "jane.doe"
}
```

### Identity Schema

#### Required

* `directory_id` (String) ID of the directory that's associated with the group.
* `group_name` (String) SAM account name of the group.
* `member_name` (String) SAM account name of the member.

#### Optional

* `account_id` (String) AWS Account where this resource is managed.
* `member_realm` (String) Domain name of the member.
* `region` (String) Region where this resource is managed.

In Terraform v1.5.0 and later, use an [`import` block](https://developer.hashicorp.com/terraform/language/import) to import a Directory Service Data Group Member using the directory ID, group name, and member name, plus the member realm for a member from another domain, separated by commas. For example:

```terraform
import {
  to = aws_directoryservicedata_group_member.example
  id = "d-1234567890,app-admins,jane.doe"
}
```

Using `terraform import`, import a Directory Service Data Group Member using the same ID. For example:

```console
% terraform import aws_directoryservicedata_group_member.example d-1234567890,app-admins,jane.doe
% terraform import aws_directoryservicedata_group_member.example d-1234567890,app-admins,jane.doe,trusted.example.com
```
