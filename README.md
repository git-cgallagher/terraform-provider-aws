<!-- Copyright IBM Corp. 2014, 2026 -->
<!-- SPDX-License-Identifier: MPL-2.0 -->

<!-- markdownlint-disable first-line-h1 no-inline-html -->
<a href="https://terraform.io">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/terraform_logo_dark.svg">
    <source media="(prefers-color-scheme: light)" srcset=".github/terraform_logo_light.svg">
    <img src=".github/terraform_logo_light.svg" alt="Terraform logo" title="Terraform" align="right" height="50">
  </picture>
</a>

# Terraform AWS Provider

> [!WARNING]
> **This is an unofficial fork, and its additions are UNTESTED against AWS.**
>
> This fork of [`hashicorp/terraform-provider-aws`](https://github.com/hashicorp/terraform-provider-aws) adds two resources that are not in the official provider:
>
> - `aws_directoryservicedata_group_member`: adds a user, group, or computer to an AWS Managed Microsoft AD group
> - `aws_directory_service_user_password`: sets (and so enables) a directory user's password, as a write-only value
>
> Their acceptance tests (`TestAccDirectoryServiceDataGroupMember_*`, `TestAccDSUserPassword_*`) are written but **have never been run**, because each one creates a real AWS Managed Microsoft AD. They have only been checked offline: unit tests, linters, `terraform plan` with a local build, and a full apply/destroy cycle against an in-memory fake of the AWS APIs. Behavior against real directories is unverified, so don't rely on them for anything that matters until the acceptance tests pass.
>
> These additions are not affiliated with or supported by HashiCorp, and have not been submitted upstream. Everything else in this repository is upstream's code. For the official provider, use [`hashicorp/aws`](https://registry.terraform.io/providers/hashicorp/aws/latest).

[![Forums][discuss-badge]][discuss]

[discuss-badge]: https://img.shields.io/badge/discuss-terraform--aws-623CE4.svg?style=flat
[discuss]: https://discuss.hashicorp.com/c/terraform-providers/tf-aws/

The [AWS Provider](https://registry.terraform.io/providers/hashicorp/aws/latest/docs) enables [Terraform](https://terraform.io) to manage [AWS](https://aws.amazon.com) resources.

- [Contributing guide](https://hashicorp.github.io/terraform-provider-aws/)
- [Quarterly development roadmap](ROADMAP.md)
- [FAQ](https://hashicorp.github.io/terraform-provider-aws/faq/)
- [Tutorials](https://learn.hashicorp.com/collections/terraform/aws-get-started)
- [discuss.hashicorp.com](https://discuss.hashicorp.com/c/terraform-providers/tf-aws/)

_**Please note:** We take Terraform's security and our users' trust very seriously. If you believe you have found a security issue in the Terraform AWS Provider, please responsibly disclose it by contacting us at security@hashicorp.com._
