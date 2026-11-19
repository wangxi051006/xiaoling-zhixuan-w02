param([Parameter(Mandatory=$true)][string]$OutDirectory)
$ErrorActionPreference = 'Stop'
if (Test-Path -LiteralPath $OutDirectory) { throw 'Output directory must be new' }
New-Item -ItemType Directory -Path $OutDirectory | Out-Null
$edgeRsa = [System.Security.Cryptography.RSA]::Create(2048)
try {
    $edgeRequest = [System.Security.Cryptography.X509Certificates.CertificateRequest]::new('CN=user.p07.test', $edgeRsa, [System.Security.Cryptography.HashAlgorithmName]::SHA256, [System.Security.Cryptography.RSASignaturePadding]::Pkcs1)
    $edgeSan = [System.Security.Cryptography.X509Certificates.SubjectAlternativeNameBuilder]::new()
    'user.p07.test', 'rider.p07.test', 'admin.p07.test' | ForEach-Object { $edgeSan.AddDnsName($_) }
    $edgeRequest.CertificateExtensions.Add($edgeSan.Build())
    $edgeCertificate = $edgeRequest.CreateSelfSigned([DateTimeOffset]::UtcNow.AddMinutes(-5), [DateTimeOffset]::UtcNow.AddHours(4))
    [IO.File]::WriteAllText((Join-Path $OutDirectory 'cert.pem'), $edgeCertificate.ExportCertificatePem())
    [IO.File]::WriteAllText((Join-Path $OutDirectory 'key.pem'), $edgeRsa.ExportPkcs8PrivateKeyPem())
    Write-Output 'Created four-hour synthetic certificate; no certificate store or hosts changes'
} finally { $edgeRsa.Dispose() }
