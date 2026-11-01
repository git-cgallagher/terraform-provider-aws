resource "aws_directory_service_user_password" "test" {
{{- template "region" }}
  directory_id        = aws_directory_service_directory.test.id
  user_name           = aws_directoryservicedata_user.test.sam_account_name
  password_wo         = "SuperSecretPassw0rd-User"
  password_wo_version = 1
}

resource "aws_directoryservicedata_user" "test" {
{{- template "region" }}
  directory_id     = aws_directory_service_directory.test.id
  sam_account_name = "tf-acc-test-user"
}

resource "aws_directory_service_directory" "test" {
{{- template "region" }}
  name                         = var.domain
  password                     = "SuperSecretPassw0rd"
  type                         = "MicrosoftAD"
  edition                      = "Standard"
  enable_directory_data_access = true

  vpc_settings {
    vpc_id     = aws_vpc.test.id
    subnet_ids = aws_subnet.test[*].id
  }
}

{{ template "acctest.ConfigVPCWithSubnets" 2 }}
