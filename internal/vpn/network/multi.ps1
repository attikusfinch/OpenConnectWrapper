param([switch]$PlanOnly)
$ErrorActionPreference = 'Stop'

function Prefix([string]$Address, [int]$Bits) {
    $ip = [Net.IPAddress]::Parse($Address)
    if ($ip.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork -or $Bits -lt 0 -or $Bits -gt 32) { throw 'Multi currently supports IPv4 routes only' }
    $bytes = $ip.GetAddressBytes()
    for ($i=0; $i -lt 4; $i++) {
        $remaining = [Math]::Min(8, [Math]::Max(0, $Bits - 8*$i))
        $bytes[$i] = $bytes[$i] -band (256 - [Math]::Pow(2, 8-$remaining))
    }
    return (($bytes -join '.') + '/' + $Bits)
}
function Normalize-Prefix([string]$Value) {
    $parts = $Value.Split('/')
    if ($parts.Count -ne 2) { throw "Invalid network: $Value" }
    return Prefix $parts[0] ([int]$parts[1])
}
function Overlap([string]$A, [string]$B) {
    $aParts=$A.Split('/'); $bParts=$B.Split('/')
    $bits=[Math]::Min([int]$aParts[1],[int]$bParts[1])
    return (Prefix $aParts[0] $bits) -eq (Prefix $bParts[0] $bits)
}
function Network-Plan {
    $full = $env:OCM_FULL -eq 'true'
    $work = @()
    for ($i=0; $i -lt [int]$env:CISCO_SPLIT_INC; $i++) {
        $address=[Environment]::GetEnvironmentVariable("CISCO_SPLIT_INC_${i}_ADDR")
        $bits=[Environment]::GetEnvironmentVariable("CISCO_SPLIT_INC_${i}_MASKLEN")
        if ($bits -eq '') { throw 'Missing split route mask length' }
        # A server may send a default or default-route pair. Only the selected
        # Internet VPN is allowed to install these.
        if ([int]$bits -gt 1) { $work += Prefix $address ([int]$bits) }
    }
    foreach ($route in @($env:OCM_ROUTES | ConvertFrom-Json)) {
        if (!$route) { continue }
        $route=Normalize-Prefix $route
        if ([int]$route.Split('/')[1] -le 1) { throw 'Use the Internet VPN selector for default routes' }
        $work += $route
    }
    $work=@($work | Sort-Object -Unique)
    if (!$full -and $work.Count -eq 0) { throw 'Server supplied no work networks. Add IPv4 networks in the profile, or select this profile as Internet VPN.' }
    $dns=@()
    foreach ($server in ($env:INTERNAL_IP4_DNS -split '\s+')) {
        if (!$server) { continue }
        $ip=[Net.IPAddress]::Parse($server)
        if ($ip.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork) { $dns += $ip.ToString() }
    }
    $dns=@($dns | Sort-Object -Unique)
    $domains=@()
    $source=@($env:CISCO_SPLIT_DNS -split '[,;\s]+') + @($env:CISCO_DEF_DOMAIN -split '[,;\s]+') + @($env:OCM_DNS_DOMAINS | ConvertFrom-Json)
    foreach ($domain in $source) {
        if (!$domain) { continue }
        $domain=$domain.Trim().TrimStart('.').TrimEnd('.').ToLowerInvariant()
        if ($domain -notmatch '^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$' -or $domain.Contains('..')) { throw "Invalid DNS domain: $domain" }
        $domains += '.'+$domain
    }
    $domains=@($domains | Sort-Object -Unique)
    if ($full -and $dns.Count -gt 0) { $domains += '.' }
    $routes=@($work)
    foreach ($server in $dns) { $routes += $server+'/32' }
    $routes=@($routes | Sort-Object -Unique)
    $excludes=@()
    if ($full) {
        $routes += '0.0.0.0/1','128.0.0.0/1'
        for ($i=0; $i -lt [int]$env:CISCO_SPLIT_EXC; $i++) {
            $address=[Environment]::GetEnvironmentVariable("CISCO_SPLIT_EXC_${i}_ADDR")
            $bits=[Environment]::GetEnvironmentVariable("CISCO_SPLIT_EXC_${i}_MASKLEN")
            if ($bits -eq '' -or [int]$bits -le 1) { throw 'Invalid split exclusion from Internet VPN' }
            $excludes += Prefix $address ([int]$bits)
        }
    }
    $warning='Multi configures IPv4. Native IPv6 remains on the normal connection.'
    if (!$full -and $dns.Count -gt 0 -and $domains.Count -eq 0) { $warning += ' Server supplied no DNS domains; add work domains in the profile for split DNS.' }
    return [pscustomobject]@{ full=$full; routes=@($routes); work_routes=$work; excludes=@($excludes | Sort-Object -Unique); dns_servers=$dns; dns_domains=$domains; warning=$warning }
}

# A pure planning entry point for offline tests; no network cmdlet runs here.
if ($PlanOnly) { Network-Plan | ConvertTo-Json -Depth 8 -Compress; return }
if ($env:reason -notin @('connect','reconnect','disconnect')) { return }
if (!$env:OCM_STATE_FILE -or !$env:OCM_NETWORK_ROOT -or $env:OCM_INTERFACE -notmatch '^OCM-[0-9a-f]{16}$') { throw 'Missing managed session context' }
$statePath=$env:OCM_STATE_FILE
$root=[IO.Path]::GetFullPath($env:OCM_NETWORK_ROOT)
if ([IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($statePath)) -ne $root) { throw 'Invalid session state path' }
$hash=[Security.Cryptography.SHA256]::Create().ComputeHash([Text.Encoding]::UTF8.GetBytes($root))
$mutexName='Local\OpenConnectMulti-'+([BitConverter]::ToString($hash).Replace('-',''))
$mutex=New-Object Threading.Mutex($false,$mutexName)
$held=$false
$script:state=$null

function Write-State {
    $temp=$statePath+'.'+$PID+'.tmp'
    [IO.File]::WriteAllText($temp,($script:state | ConvertTo-Json -Depth 12),[Text.UTF8Encoding]::new($false))
    if ([IO.File]::Exists($statePath)) {
        $backup=$statePath+'.bak'
        [IO.File]::Replace($temp,$statePath,$backup)
        [IO.File]::Delete($backup)
    } else { [IO.File]::Move($temp,$statePath) }
}
function Other-States {
    return @(Get-ChildItem -LiteralPath $root -Filter '*.json' | Where-Object { $_.FullName -ne $statePath } | ForEach-Object {
        $other=Get-Content -Raw -LiteralPath $_.FullName | ConvertFrom-Json
        if ($other.ready -or @($other.created_routes).Count -gt 0) { $other }
    })
}
function Remove-Route($route) {
    Get-NetRoute -DestinationPrefix $route.prefix -InterfaceIndex $route.index -PolicyStore ActiveStore -ErrorAction SilentlyContinue |
        Where-Object { $_.NextHop -eq $route.hop } | Remove-NetRoute -Confirm:$false -ErrorAction Stop
}
function Clear-Owned {
    if (!$script:state) { return }
    $problems=@()
    try {
        # The comment also recovers a rule created just before a forced stop,
        # before Add-DnsClientNrptRule's generated Name reached the journal.
        Get-DnsClientNrptRule | Where-Object { $_.Comment -eq $env:OCM_INTERFACE } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force }
    } catch { $problems += $_.Exception.Message }
    foreach ($route in @($script:state.created_routes)) {
        try { Remove-Route $route } catch { $problems += $_.Exception.Message }
    }
    if ($script:state.gateway_owned -and $script:state.gateway_route) {
        $shared=@(Other-States | Where-Object { $_.gateway_route -and $_.gateway_route.prefix -eq $script:state.gateway_route.prefix })
        if ($shared.Count -eq 0) { try { Remove-Route $script:state.gateway_route } catch { $problems += $_.Exception.Message } }
    }
    $adapter=Get-NetAdapter -Name $env:OCM_INTERFACE -ErrorAction SilentlyContinue
    if ($adapter -and $adapter.ifIndex -eq $script:state.index) {
        try {
            Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue |
                Where-Object { $_.IPAddress -eq $script:state.address } | Remove-NetIPAddress -Confirm:$false
        } catch { $problems += $_.Exception.Message }
    }
    if ($problems.Count) { throw ($problems -join '; ') }
    $script:state.ready=$false
    $script:state.created_routes=@(); $script:state.dns_rules=@(); $script:state.routes=@(); $script:state.dns_domains=@(); $script:state.gateway_owned=$false
    Write-State
}
function Add-OwnedRoute([string]$Prefix,[int]$Index,[string]$Hop,[int]$Metric=5) {
    $existing=@(Get-NetRoute -DestinationPrefix $Prefix -InterfaceIndex $Index -PolicyStore ActiveStore -ErrorAction SilentlyContinue | Where-Object { $_.NextHop -eq $Hop })
    if ($existing.Count) { return }
    $entry=[pscustomobject]@{prefix=$Prefix;index=$Index;hop=$Hop}
    # Journal before mutation so even a forced process stop can be cleaned up.
    $script:state.created_routes += $entry
    Write-State
    New-NetRoute -DestinationPrefix $Prefix -InterfaceIndex $Index -NextHop $Hop -RouteMetric $Metric -PolicyStore ActiveStore | Out-Null
}
try {
    try { $held=$mutex.WaitOne(60000) } catch [Threading.AbandonedMutexException] { $held=$true }
    if (!$held) { throw 'Timed out waiting for network configuration lock' }
    if (Test-Path -LiteralPath $statePath) { $script:state=Get-Content -Raw -LiteralPath $statePath | ConvertFrom-Json }
    if ($env:reason -eq 'disconnect') { Clear-Owned; return }
    if ($script:state) { Clear-Owned }
    $plan=Network-Plan
    $address=[Net.IPAddress]::Parse($env:INTERNAL_IP4_ADDRESS).ToString()
    $index=[int]$env:TUNIDX
    $adapter=Get-NetAdapter -IncludeHidden | Where-Object { $_.ifIndex -eq $index }
    if ($adapter.Name -ne $env:OCM_INTERFACE) { throw 'Tunnel interface does not match this profile' }
    $others=@(Other-States)
    foreach ($other in $others) {
        if ($other.address -eq $address) { throw "Another VPN uses tunnel address $address" }
        if ($plan.full -and $other.full) { throw 'Only one Internet VPN can run in Multi mode' }
        foreach ($route in $plan.routes | Where-Object { $_ -notin @('0.0.0.0/1','128.0.0.0/1') }) {
            foreach ($existing in $other.routes | Where-Object { $_ -notin @('0.0.0.0/1','128.0.0.0/1') }) {
                if (Overlap $route $existing) { throw "Work network conflict: $route overlaps $existing on $($other.interface). Disconnect that profile or narrow its work networks." }
            }
        }
        foreach ($domain in $plan.dns_domains) {
            if ($domain -in $other.dns_domains) { throw "DNS domain conflict: $domain is already assigned to $($other.interface)" }
        }
        foreach ($excluded in $other.applied_excludes) {
            foreach ($route in $plan.routes | Where-Object { $_ -notin @('0.0.0.0/1','128.0.0.0/1') }) {
                if ((Overlap $route $excluded) -and [int]$excluded.Split('/')[1] -gt [int]$route.Split('/')[1]) { throw "Work route $route conflicts with a more specific Internet VPN exclusion $excluded. Connect work profiles first." }
            }
        }
    }
    # Keep every VPN endpoint on the original Internet path, even after a full
    # tunnel is running. Never choose another managed tunnel as its gateway.
    $default=Get-NetRoute -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -PolicyStore ActiveStore |
        Where-Object { $_.InterfaceAlias -notlike 'OCM-*' } |
        Sort-Object @{Expression={ $_.RouteMetric + (Get-NetIPInterface -InterfaceIndex $_.InterfaceIndex -AddressFamily IPv4).InterfaceMetric }} | Select-Object -First 1
    if (!$default) { throw 'No original IPv4 Internet gateway found' }
    $gateway=[Net.IPAddress]::Parse($env:VPNGATEWAY)
    if ($gateway.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) { throw 'Multi currently requires an IPv4 VPN gateway' }
    $gwRoute=[pscustomobject]@{prefix=$gateway.ToString()+'/32';index=[int]$default.InterfaceIndex;hop=[string]$default.NextHop}
    $sameGateway=@($others | Where-Object { $_.gateway_route -and $_.gateway_route.prefix -eq $gwRoute.prefix })
    $exists=@(Get-NetRoute -DestinationPrefix $gwRoute.prefix -InterfaceIndex $gwRoute.index -PolicyStore ActiveStore -ErrorAction SilentlyContinue | Where-Object { $_.NextHop -eq $gwRoute.hop })
    $script:state=[pscustomobject]@{ready=$false;error='';interface=$env:OCM_INTERFACE;index=$index;address=$address;full=$plan.full;routes=$plan.routes;dns_servers=$plan.dns_servers;dns_domains=$plan.dns_domains;warning=$plan.warning;created_routes=@();dns_rules=@();applied_excludes=@();gateway_route=$gwRoute;gateway_owned=($exists.Count -eq 0 -or @($sameGateway | Where-Object gateway_owned).Count -gt 0)}
    Write-State
    if (!$exists.Count) { New-NetRoute -DestinationPrefix $gwRoute.prefix -InterfaceIndex $gwRoute.index -NextHop $gwRoute.hop -RouteMetric 1 -PolicyStore ActiveStore | Out-Null }
    Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -Dhcp Disabled -AutomaticMetric Disabled -InterfaceMetric 50
    New-NetIPAddress -InterfaceIndex $index -IPAddress $address -PrefixLength 32 -AddressFamily IPv4 -PolicyStore ActiveStore | Out-Null
    if ($env:INTERNAL_IP4_MTU) { Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -NlMtuBytes ([int]$env:INTERNAL_IP4_MTU) }
    foreach ($route in $plan.routes) { Add-OwnedRoute $route $index '0.0.0.0' }
    foreach ($exclude in $plan.excludes) {
        $conflict=@($others | ForEach-Object { $_.routes } | Where-Object { $_ -notin @('0.0.0.0/1','128.0.0.0/1') -and (Overlap $_ $exclude) })
        if (!$conflict.Count) { Add-OwnedRoute $exclude $gwRoute.index $gwRoute.hop 100; $script:state.applied_excludes += $exclude; Write-State }
    }
    if ($plan.dns_servers.Count -gt 0 -and $plan.dns_domains.Count -gt 0) {
        foreach ($domain in $plan.dns_domains) {
            $existingRules=@(Get-DnsClientNrptRule | Where-Object { $domain -in $_.Namespace })
            if ($existingRules.Count) { throw "DNS policy already exists for $domain; use a different work domain" }
            $rule=Add-DnsClientNrptRule -Namespace $domain -NameServers $plan.dns_servers -Comment $env:OCM_INTERFACE -PassThru
            $script:state.dns_rules += $rule.Name
            Write-State
        }
    }
    $script:state.ready=$true
    Write-State
} catch {
    $problem=$_.Exception.Message
    if ($held) {
        if (!$script:state) { $script:state=[pscustomobject]@{ready=$false;error='';interface=$env:OCM_INTERFACE;routes=@();dns_servers=@();dns_domains=@();created_routes=@();dns_rules=@();gateway_owned=$false} }
        try { Clear-Owned } catch { $problem += '; cleanup: '+$_.Exception.Message }
        $script:state.error=$problem; $script:state.ready=$false
        Write-State
    }
    Write-Error $problem
    exit 1
} finally {
    if ($held) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
}
