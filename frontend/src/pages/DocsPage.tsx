import { useState, useRef, useEffect } from 'react'
import { BookOpen, Check, Copy } from 'lucide-react'
import styles from './DocsPage.module.css'

interface CodeExample { label?: string; lang: string; content: string }
interface FormatSection { title: string; text?: string; note?: string; codes: CodeExample[] }
interface Format {
  id: string
  name: string
  icon: string
  iconUrl?: string
  description: string
  sections: (base: string) => FormatSection[]
}
interface StepProps {
  num: number
  title: string
  text: string
  screenshot?: { src: string; alt: string; caption?: string }
  code?: { lang: string; content: string }
  note?: string
}

function CodeBlock({ lang, content }: { lang: string; content: string }) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (timerRef.current) clearTimeout(timerRef.current) }, [])
  const copy = () => {
    void navigator.clipboard.writeText(content).then(() => {
      setCopied(true)
      if (timerRef.current) clearTimeout(timerRef.current)
      timerRef.current = setTimeout(() => setCopied(false), 2000)
    })
  }
  return (
    <div className={styles.codeBlock}>
      <div className={styles.codeHeader}>
        <span className={styles.codeLang}>{lang}</span>
        <button className={`${styles.copyBtn} ${copied ? styles.copied : ''}`} onClick={copy}>
          {copied ? <Check size={11} /> : <Copy size={11} />}
          {copied ? 'Copied!' : 'Copy'}
        </button>
      </div>
      <div className={styles.codeBody}>
        <pre>{content}</pre>
      </div>
    </div>
  )
}

function UrlBlock({ url }: { url: string }) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (timerRef.current) clearTimeout(timerRef.current) }, [])
  return (
    <div className={styles.urlBlock}>
      <span className={styles.urlValue}>{url}</span>
      <button
        className={styles.urlCopyBtn}
        onClick={() => { void navigator.clipboard.writeText(url).then(() => { setCopied(true); if (timerRef.current) clearTimeout(timerRef.current); timerRef.current = setTimeout(() => setCopied(false), 2000) }) }}
      >
        {copied ? <Check size={11} /> : <Copy size={11} />}
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  )
}

function SectionBlock({ section }: { section: FormatSection }) {
  return (
    <div>
      <p className={styles.blockTitle}>{section.title}</p>
      {section.text && <p className={styles.blockText}>{section.text}</p>}
      {section.note && <div className={styles.noteBox}>⚠ {section.note}</div>}
      {section.codes.map((c, i) => (
        <div key={i}>
          {c.label && <p className={styles.codeLabel}>{c.label}</p>}
          <CodeBlock lang={c.lang} content={c.content} />
        </div>
      ))}
    </div>
  )
}

function ScreenshotPlaceholder({ alt, src }: { alt: string; src: string }) {
  const filename = src.split('/').pop() ?? src
  return (
    <div className={styles.screenshotPlaceholder}>
      <span className={styles.screenshotPlaceholderLabel}>📸 Screenshot</span>
      <span className={styles.screenshotPlaceholderName}>{alt}</span>
      <span className={styles.screenshotPlaceholderPath}>
        frontend/public/docs/screenshots/{filename}
      </span>
    </div>
  )
}

function Screenshot({ src, alt, caption }: { src: string; alt: string; caption?: string }) {
  const [failed, setFailed] = useState(false)
  if (failed) return <ScreenshotPlaceholder alt={alt} src={src} />
  return (
    <div className={styles.screenshotWrap}>
      <img
        src={src}
        alt={alt}
        className={styles.screenshotImg}
        onError={() => setFailed(true)}
      />
      {caption && <p className={styles.screenshotCaption}>{caption}</p>}
    </div>
  )
}

export function Step({ num, title, text, screenshot, code, note }: StepProps) {
  return (
    <div className={styles.step}>
      <div className={styles.stepNum}>{num}</div>
      <div className={styles.stepBody}>
        <p className={styles.stepTitle}>{title}</p>
        <p className={styles.stepText}>{text}</p>
        {note && <div className={styles.noteBox}>⚠ {note}</div>}
        {code && <CodeBlock lang={code.lang} content={code.content} />}
        {screenshot && <Screenshot {...screenshot} />}
      </div>
    </div>
  )
}

const FORMATS: Format[] = [
  {
    id: 'maven',
    name: 'Maven 2/3',
    icon: '☕',
    iconUrl: 'https://cdn.simpleicons.org/apachemaven/C71A36',
    description: 'Host, proxy, and group Maven repositories for Java artifacts (JAR, WAR, POM files). Fully compatible with Maven 2 and Maven 3.',
    sections: (base) => [
      {
        title: 'Repository URL',
        text: 'Use these endpoints in settings.xml or pom.xml:',
        codes: [{ lang: 'text', content: `${base}/repository/maven-releases/\n${base}/repository/maven-snapshots/\n${base}/repository/maven-central/   ← proxy cache` }],
      },
      {
        title: 'Configure ~/.m2/settings.xml',
        text: 'Route all Maven traffic through Nexspence and set credentials:',
        codes: [{ lang: 'xml', content: `<settings>
  <servers>
    <server>
      <id>nexspence</id>
      <username>admin</username>
      <password>admin123</password>
    </server>
  </servers>
  <mirrors>
    <mirror>
      <id>nexspence</id>
      <url>${base}/repository/maven-public/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>` }],
      },
      {
        title: 'Publish an Artifact',
        codes: [
          { label: 'Using mvn deploy plugin:', lang: 'bash', content: `mvn deploy:deploy-file \\
  -DrepositoryId=nexspence \\
  -Durl=${base}/repository/maven-releases/ \\
  -Dfile=myapp-1.0.jar \\
  -DgroupId=com.example \\
  -DartifactId=myapp \\
  -Dversion=1.0` },
          { label: 'Using curl (direct PUT):', lang: 'bash', content: `curl -u admin:admin123 \\
  -T myapp-1.0.jar \\
  "${base}/repository/maven-releases/com/example/myapp/1.0/myapp-1.0.jar"` },
        ],
      },
      {
        title: 'Download an Artifact',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/maven-releases/com/example/myapp/1.0/myapp-1.0.jar"` }],
      },
    ],
  },
  {
    id: 'npm',
    name: 'npm',
    icon: '📦',
    iconUrl: 'https://cdn.simpleicons.org/npm/CB3837',
    description: 'Host and proxy npm packages. Supports npm publish, install, and the full npm registry protocol.',
    sections: (base) => [
      {
        title: 'Repository URLs',
        codes: [{ lang: 'text', content: `${base}/repository/npm-hosted/   ← publish target\n${base}/repository/npm-proxy/    ← proxy to npmjs.com\n${base}/repository/npm-group/    ← combined group` }],
      },
      {
        title: 'Configure .npmrc',
        text: 'Add to your project .npmrc or ~/.npmrc:',
        codes: [{ lang: 'ini', content: `registry=${base}/repository/npm-group/
//${base.replace(/^https?:\/\//, '')}/repository/npm-hosted/:_authToken=nxs_your_token_here` }],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Using npm publish:', lang: 'bash', content: `npm publish --registry ${base}/repository/npm-hosted/` },
          { label: 'Using curl (upload tarball):', lang: 'bash', content: `npm pack
curl -u admin:admin123 \\
  -H "Content-Type: application/octet-stream" \\
  -T mypackage-1.0.0.tgz \\
  "${base}/repository/npm-hosted/mypackage/-/mypackage-1.0.0.tgz"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using npm:', lang: 'bash', content: `npm install mypackage --registry ${base}/repository/npm-group/` },
          { label: 'Download tarball with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/npm-group/mypackage/-/mypackage-1.0.0.tgz"` },
        ],
      },
    ],
  },
  {
    id: 'pypi',
    name: 'PyPI',
    icon: '🐍',
    iconUrl: 'https://cdn.simpleicons.org/pypi/3775A9',
    description: 'Host Python packages and proxy PyPI. Supports pip, twine, and the PyPI Simple API.',
    sections: (base) => {
      const host = base.replace(/^https?:\/\//, '').split(':')[0]
      return [
        {
          title: 'Repository URLs',
          codes: [{ lang: 'text', content: `${base}/repository/pypi-hosted/\n${base}/repository/pypi-proxy/simple/\n${base}/repository/pypi-group/simple/` }],
        },
        {
          title: 'Configure pip (~/.config/pip/pip.conf)',
          codes: [{ lang: 'ini', content: `[global]
index-url = ${base}/repository/pypi-group/simple/
trusted-host = ${host}` }],
        },
        {
          title: 'Publish a Package',
          codes: [
            { label: 'Using twine:', lang: 'bash', content: `python -m build
twine upload \\
  --repository-url ${base}/repository/pypi-hosted/ \\
  --username admin \\
  --password admin123 \\
  dist/*` },
            { label: 'Using curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -F "content=@dist/mypackage-1.0.0.tar.gz" \\
  "${base}/repository/pypi-hosted/"` },
          ],
        },
        {
          title: 'Install a Package',
          codes: [{ lang: 'bash', content: `pip install mypackage \\
  --index-url ${base}/repository/pypi-group/simple/ \\
  --trusted-host ${host}` }],
        },
      ]
    },
  },
  {
    id: 'docker',
    name: 'Docker',
    icon: '🐳',
    iconUrl: 'https://cdn.simpleicons.org/docker/2496ED',
    description: 'Container images over the OCI Distribution Spec v2. Supports docker pull/push, image tagging, and multi-arch manifests. For charts and ORAS artifacts, see OCI Artifacts.',
    sections: (base) => {
      const regHost = base.replace(/^https?:\/\//, '')
      return [
        {
          title: 'Registry Host',
          text: 'Docker uses the host:port directly — no /repository/ prefix. The image name includes the repository.',
          codes: [{ lang: 'text', content: `${regHost}/<repository-name>/<image-name>:<tag>` }],
        },
        {
          title: 'Login',
          codes: [{ lang: 'bash', content: `docker login ${regHost} -u admin -p admin123
# Or with an API token:
docker login ${regHost} -u admin -p nxs_your_token_here` }],
        },
        {
          title: 'Push an Image',
          codes: [{ lang: 'bash', content: `# Tag your local image (docker-hosted is the Nexspence repository name)
docker tag myapp:latest ${regHost}/docker-hosted/myapp:latest

# Push to Nexspence
docker push ${regHost}/docker-hosted/myapp:latest` }],
        },
        {
          title: 'Pull an Image',
          codes: [
            { label: 'Using docker pull:', lang: 'bash', content: `docker pull ${regHost}/docker-hosted/myapp:latest` },
            { label: 'Inspect manifest with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/v2/docker-hosted/myapp/manifests/latest" \\
  -H "Accept: application/vnd.docker.distribution.manifest.v2+json"` },
          ],
        },
        {
          title: 'List Tags',
          codes: [{ lang: 'bash', content: `curl -u admin:admin123 "${base}/v2/myapp/tags/list"` }],
        },
      ]
    },
  },
  {
    id: 'oci',
    name: 'OCI Artifacts',
    icon: '📦',
    description: 'The same OCI Distribution registry, for everything that is not a container image: Helm charts pushed over oci://, ORAS artifacts such as WASM modules and ML models, and the cosign signatures, SBOMs and attestations attached to them.',
    sections: (base) => {
      const regHost = base.replace(/^https?:\/\//, '')
      return [
        {
          title: 'Registry Host',
          text: 'Like Docker, OCI clients address the host:port directly — no /repository/ prefix. The artifact name includes the Nexspence repository name.',
          codes: [{ lang: 'text', content: `${regHost}/<repository-name>/<artifact-name>:<tag>` }],
        },
        {
          title: 'Login',
          codes: [
            { label: 'Using oras:', lang: 'bash', content: `oras login ${regHost} -u admin -p admin123` },
            { label: 'Using helm:', lang: 'bash', content: `helm registry login ${regHost} -u admin -p admin123` },
          ],
        },
        {
          title: 'Push a Helm Chart',
          text: 'helm push takes an already-packaged chart and an oci:// target. The chart name and version come from the archive, so the target is the namespace it goes in — not the full reference.',
          codes: [{ lang: 'bash', content: `helm package mychart/

helm push mychart-1.2.3.tgz oci://${regHost}/oci-hosted/charts` }],
        },
        {
          title: 'Install a Helm Chart',
          codes: [
            { label: 'Install directly from the registry:', lang: 'bash', content: `helm install my-release \\
  oci://${regHost}/oci-hosted/charts/mychart --version 1.2.3` },
            { label: 'Or pull the archive first:', lang: 'bash', content: `helm pull oci://${regHost}/oci-hosted/charts/mychart --version 1.2.3` },
          ],
        },
        {
          title: 'Push an Artifact with ORAS',
          text: 'Each file is pushed with its own layer media type, and --artifact-type declares what the whole thing is. Nexspence records both and shows the artifact type in the browse tree.',
          codes: [{ lang: 'bash', content: `oras push ${regHost}/oci-hosted/my-module:1.0 \\
  --artifact-type application/vnd.wasm.config.v1+json \\
  module.wasm:application/vnd.wasm.content.layer.v1+wasm` }],
        },
        {
          title: 'Pull an Artifact',
          codes: [
            { label: 'Using oras pull:', lang: 'bash', content: `oras pull ${regHost}/oci-hosted/my-module:1.0` },
            { label: 'Inspect the manifest with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/v2/oci-hosted/my-module/manifests/1.0" \\
  -H "Accept: application/vnd.oci.image.manifest.v1+json"` },
          ],
        },
        {
          title: 'Signatures, SBOMs and Attestations',
          text: 'Artifacts attached with a subject are discoverable through the referrers API.',
          codes: [
            { label: 'Sign a chart or artifact:', lang: 'bash', content: `cosign sign --key cosign.key ${regHost}/oci-hosted/my-module:1.0` },
            { label: 'List what is attached to it:', lang: 'bash', content: `oras discover ${regHost}/oci-hosted/my-module:1.0` },
          ],
        },
      ]
    },
  },
  {
    id: 'go',
    name: 'Go Modules',
    icon: '🔵',
    iconUrl: 'https://cdn.simpleicons.org/go/00ADD8',
    description: 'GOPROXY v2 protocol. Cache and proxy Go modules with version resolution and mod file serving.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/go-proxy/` }],
      },
      {
        title: 'Configure Go Proxy',
        codes: [
          { label: 'Set environment variable:', lang: 'bash', content: `export GOPROXY="${base}/repository/go-proxy/|direct"
export GONOSUMDB="*"    # bypass sum database for all modules (self-signed TLS)` },
          { label: 'Persist with go env -w:', lang: 'bash', content: `go env -w GOPROXY="${base}/repository/go-proxy/|direct"` },
        ],
      },
      {
        title: 'Download a Module',
        codes: [
          { label: 'Using go get:', lang: 'bash', content: `go get github.com/some/module@v1.2.3` },
          { label: 'GOPROXY v2 protocol via curl:', lang: 'bash', content: `# List available versions
curl -u admin:admin123 \\
  "${base}/repository/go-proxy/github.com/some/module/@v/list"

# Download module zip
curl -u admin:admin123 \\
  -O "${base}/repository/go-proxy/github.com/some/module/@v/v1.2.3.zip"

# Fetch go.mod
curl -u admin:admin123 \\
  "${base}/repository/go-proxy/github.com/some/module/@v/v1.2.3.mod"` },
        ],
      },
    ],
  },
  {
    id: 'nuget',
    name: 'NuGet',
    icon: '💜',
    iconUrl: 'https://cdn.simpleicons.org/nuget/004880',
    description: 'NuGet v2/v3 repository for .NET packages. Compatible with dotnet CLI, nuget.exe, and MSBuild PackageReference.',
    sections: (base) => [
      {
        title: 'Repository URLs',
        codes: [{ lang: 'text', content: `${base}/repository/nuget-hosted/index.json    ← v3 API\n${base}/repository/nuget-hosted/              ← v2 OData\n${base}/repository/nuget-group/index.json     ← group` }],
      },
      {
        title: 'Configure nuget.config',
        codes: [{ lang: 'xml', content: `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <add key="nexspence" value="${base}/repository/nuget-group/index.json" />
  </packageSources>
  <packageSourceCredentials>
    <nexspence>
      <add key="Username" value="admin" />
      <add key="ClearTextPassword" value="admin123" />
    </nexspence>
  </packageSourceCredentials>
</configuration>` }],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Using dotnet CLI:', lang: 'bash', content: `dotnet nuget push mypackage.1.0.0.nupkg \\
  --source ${base}/repository/nuget-hosted/ \\
  --api-key admin:admin123` },
          { label: 'Using curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -F "package=@mypackage.1.0.0.nupkg" \\
  "${base}/repository/nuget-hosted/"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [{ lang: 'bash', content: `dotnet add package MyPackage \\
  --source ${base}/repository/nuget-group/index.json` }],
      },
    ],
  },
  {
    id: 'raw',
    name: 'Raw',
    icon: '📄',
    description: 'Generic file storage at any path. Ideal for scripts, release tarballs, configuration files, and binary assets.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/raw-hosted/<path/to/your/file>` }],
      },
      {
        title: 'Upload a File',
        codes: [
          { label: 'Using curl PUT:', lang: 'bash', content: `curl -u admin:admin123 \\
  -T myfile.tar.gz \\
  "${base}/repository/raw-hosted/releases/v1.0/myfile.tar.gz"` },
          { label: 'Upload binary with --data-binary:', lang: 'bash', content: `curl -u admin:admin123 \\
  -X PUT \\
  --data-binary @deploy.sh \\
  "${base}/repository/raw-hosted/scripts/deploy.sh"` },
        ],
      },
      {
        title: 'Download a File',
        codes: [
          { label: 'Using curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/raw-hosted/releases/v1.0/myfile.tar.gz"` },
          { label: 'Using wget:', lang: 'bash', content: `wget --user=admin --password=admin123 \\
  "${base}/repository/raw-hosted/scripts/deploy.sh"` },
        ],
      },
      {
        title: 'List Files',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/service/rest/v1/components?repository=raw-hosted" \\
  | python3 -m json.tool` }],
      },
    ],
  },
  {
    id: 'helm',
    name: 'Helm',
    icon: '⚓',
    iconUrl: 'https://cdn.simpleicons.org/helm/0F1689',
    description: 'Helm chart repository for Kubernetes. Serves Helm charts with auto-generated index.yaml.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/helm-hosted/` }],
      },
      {
        title: 'Add Repository',
        codes: [{ lang: 'bash', content: `helm repo add nexspence ${base}/repository/helm-hosted/ \\
  --username admin \\
  --password admin123

helm repo update` }],
      },
      {
        title: 'Publish a Chart',
        codes: [
          { label: 'Package then upload with curl:', lang: 'bash', content: `helm package mychart/
curl -u admin:admin123 \\
  -T mychart-1.0.0.tgz \\
  "${base}/repository/helm-hosted/mychart-1.0.0.tgz"` },
          { label: 'Using helm cm-push plugin:', lang: 'bash', content: `helm plugin install https://github.com/chartmuseum/helm-push
helm cm-push mychart/ nexspence` },
        ],
      },
      {
        title: 'Install a Chart',
        codes: [
          { label: 'Using helm install:', lang: 'bash', content: `helm install my-release nexspence/mychart --version 1.0.0` },
          { label: 'Download chart with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/helm-hosted/mychart-1.0.0.tgz"` },
        ],
      },
    ],
  },
  {
    id: 'cargo',
    name: 'Cargo (Rust)',
    icon: '🦀',
    iconUrl: 'https://cdn.simpleicons.org/rust/b7410e',
    description: 'Rust Cargo sparse registry. Supports cargo publish, cargo add, and the sparse index protocol.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/cargo-hosted/` }],
      },
      {
        title: 'Configure ~/.cargo/config.toml',
        codes: [{ lang: 'toml', content: `[registries.nexspence]
index = "sparse+${base}/repository/cargo-hosted/"
credential-provider = "cargo:token"

[registry]
default = "nexspence"` }],
      },
      {
        title: 'Authenticate',
        codes: [{ lang: 'bash', content: `cargo login --registry nexspence nxs_your_token_here` }],
      },
      {
        title: 'Publish a Crate',
        codes: [
          { label: 'Using cargo publish:', lang: 'bash', content: `cargo publish --registry nexspence` },
        ],
      },
      {
        title: 'Add a Dependency',
        codes: [{ lang: 'bash', content: `cargo add mycrate --registry nexspence` }],
      },
    ],
  },
  {
    id: 'apt',
    name: 'Apt / Debian',
    icon: '🐧',
    iconUrl: 'https://cdn.simpleicons.org/debian/A81D33',
    description: 'Debian APT repository. Serves .deb packages with auto-generated Packages index.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/apt-hosted/` }],
      },
      {
        title: 'Configure /etc/apt/sources.list.d/nexspence.list',
        codes: [{ lang: 'bash', content: `echo "deb [trusted=yes] ${base}/repository/apt-hosted/ focal main" \\
  | sudo tee /etc/apt/sources.list.d/nexspence.list

sudo apt-get update` }],
        note: 'Replace "focal main" with your distribution codename and component (e.g. "jammy main", "bullseye contrib").',
      },
      {
        title: 'Publish a .deb Package',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  -H "Content-Type: application/octet-stream" \\
  -T mypackage_1.0_amd64.deb \\
  "${base}/repository/apt-hosted/"` }],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using apt-get:', lang: 'bash', content: `sudo apt-get install mypackage` },
          { label: 'Direct .deb download and install:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/apt-hosted/pool/main/m/mypackage/mypackage_1.0_amd64.deb"
sudo dpkg -i mypackage_1.0_amd64.deb` },
        ],
      },
    ],
  },
  {
    id: 'yum',
    name: 'Yum / RPM',
    icon: '🔴',
    iconUrl: 'https://cdn.simpleicons.org/fedora/51A2DA',
    description: 'Yum/DNF RPM repository. Serves RPM packages with auto-generated repomd.xml metadata.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/yum-hosted/` }],
      },
      {
        title: 'Configure /etc/yum.repos.d/nexspence.repo',
        codes: [{ lang: 'ini', content: `[nexspence]
name=Nexspence Repository
baseurl=${base}/repository/yum-hosted/
enabled=1
gpgcheck=0
username=admin
password=admin123` }],
      },
      {
        title: 'Publish an RPM Package',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  -H "Content-Type: application/x-rpm" \\
  -T mypackage-1.0-1.x86_64.rpm \\
  "${base}/repository/yum-hosted/"` }],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using yum/dnf:', lang: 'bash', content: `sudo yum install mypackage
# or
sudo dnf install mypackage` },
          { label: 'Direct RPM download and install:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/yum-hosted/mypackage-1.0-1.x86_64.rpm"
sudo rpm -ivh mypackage-1.0-1.x86_64.rpm` },
        ],
      },
    ],
  },
  {
    id: 'conan',
    name: 'Conan C/C++',
    icon: '🔧',
    iconUrl: 'https://cdn.simpleicons.org/conan/6699CB',
    description: 'Conan package manager repository for C and C++ libraries. Conan 2.x is supported over the v2 revisions protocol — login, upload and install — and Conan 1.x over the v1 API.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/conan-hosted/` }],
      },
      {
        title: 'Add Remote and Authenticate',
        codes: [
          { label: 'Conan 2.x:', lang: 'bash', content: `conan remote add nexspence ${base}/repository/conan-hosted/
conan remote login nexspence admin -p admin123` },
          { label: 'Conan 1.x:', lang: 'bash', content: `conan remote add nexspence ${base}/repository/conan-hosted/
conan user admin -p admin123 -r nexspence` },
        ],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Conan 2.x:', lang: 'bash', content: `conan create .
conan upload "mylib/1.0" -r=nexspence --confirm` },
          { label: 'Conan 1.x:', lang: 'bash', content: `conan upload mylib/1.0@user/stable -r nexspence --all` },
          { label: 'Get upload URLs with curl (v1 API):', lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/repository/conan-hosted/v1/conans/mylib/1.0/user/stable/upload_urls"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Conan 2.x:', lang: 'bash', content: `conan install --requires="mylib/1.0" -r=nexspence` },
          { label: 'Conan 1.x:', lang: 'bash', content: `conan install mylib/1.0@user/stable -r nexspence` },
          { label: 'Get download URLs with curl (v1 API):', lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/repository/conan-hosted/v1/conans/mylib/1.0/user/stable/download_urls"` },
        ],
        note: 'Searching and deleting over the remote (conan search / conan list / conan remove) are not implemented yet — resolve and download work, so upload and install round trips are unaffected.',
      },
    ],
  },
  {
    id: 'rubygems',
    name: 'RubyGems',
    icon: '💎',
    iconUrl: 'https://cdn.simpleicons.org/rubygems/E9573F',
    description: 'RubyGems repository for Ruby packages. Serves the compact index Bundler resolves against, gem downloads, and gem push/yank for hosted repositories.',
    sections: (base) => [
      {
        title: 'Bundler',
        text: 'Point your Gemfile at the repository:',
        codes: [{ lang: 'ruby', content: `source "${base}/repository/gems-hosted" do\n  gem "rails"\nend` }],
      },
      {
        title: 'gem CLI',
        text: 'Install directly, or add the repository as a source:',
        codes: [{ lang: 'bash', content: `gem sources --add ${base}/repository/gems-hosted/\ngem install rails --source ${base}/repository/gems-hosted/` }],
      },
      {
        title: 'Publish',
        text: 'The gem client authenticates pushes with an API key; use your credentials encoded as HTTP Basic:',
        codes: [{ lang: 'bash', content: `printf ':rubygems_api_key: Basic %s\\n' "$(printf 'USER:PASSWORD' | base64)" > ~/.gem/credentials\nchmod 600 ~/.gem/credentials\ngem push my-gem-1.0.0.gem --host ${base}/repository/gems-hosted` }],
      },
    ],
  },
  {
    id: 'conda',
    name: 'Conda',
    icon: '🐍',
    iconUrl: 'https://cdn.simpleicons.org/anaconda/44A833',
    description: 'Conda channel repository for Python and data science packages. Serves repodata.json index and .conda/.tar.bz2 binaries organized by platform subdirectory.',
    sections: (base) => [
      {
        title: 'Channel URL',
        text: 'Channels are organized by platform. Replace the subdirectory with your target architecture:',
        codes: [{ lang: 'text', content: `${base}/repository/conda-hosted/linux-64/\n${base}/repository/conda-hosted/osx-arm64/\n${base}/repository/conda-hosted/win-64/\n${base}/repository/conda-hosted/noarch/      ← platform-independent packages` }],
      },
      {
        title: 'Configure ~/.condarc',
        text: 'Add Nexspence as a channel. Prepend it so your hosted packages take priority:',
        codes: [{ lang: 'yaml', content: `channels:\n  - ${base}/repository/conda-hosted/\n  - defaults\nssl_verify: true` }],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Build the package:', lang: 'bash', content: `conda build myrecipe/` },
          { label: 'Upload the built .conda file:', lang: 'bash', content: `PKG=$(conda build myrecipe/ --output)\n\ncurl -u admin:admin123 \\\n  -T "$PKG" \\\n  "${base}/repository/conda-hosted/linux-64/$(basename $PKG)"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using conda:', lang: 'bash', content: `conda install mypackage \\\n  -c ${base}/repository/conda-hosted/ \\\n  --override-channels` },
          { label: 'Using mamba (faster resolver):', lang: 'bash', content: `mamba install mypackage \\\n  -c ${base}/repository/conda-hosted/ \\\n  --override-channels` },
          { label: 'Direct download with curl:', lang: 'bash', content: `curl -u admin:admin123 \\\n  -O "${base}/repository/conda-hosted/linux-64/mypackage-1.0.0-py311_0.conda"` },
        ],
      },
    ],
  },
  {
    id: 'terraform',
    name: 'Terraform',
    icon: '🏗',
    iconUrl: 'https://cdn.simpleicons.org/terraform/7B42BC',
    description: 'Terraform Registry Protocol v1 for providers and modules. Supports service discovery at /.well-known/terraform.json, version listing, and binary hosting for both hosted and proxy types.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/terraform-hosted/\nService discovery: ${base}/.well-known/terraform.json` }],
      },
      {
        title: 'Configure .terraformrc',
        text: 'Tell Terraform to use Nexspence for provider installation. Add to ~/.terraformrc (macOS/Linux) or %APPDATA%/terraform.rc (Windows):',
        codes: [{ lang: 'hcl', content: `credentials "${base.replace(/^https?:\/\//, '')}" {\n  token = "nxs_your_token_here"\n}\n\nprovider_installation {\n  network_mirror {\n    url = "${base}/repository/terraform-hosted/"\n  }\n}` }],
      },
      {
        title: 'Use a Provider',
        text: 'Reference the provider in your Terraform configuration, then run terraform init:',
        codes: [
          { label: 'main.tf:', lang: 'hcl', content: `terraform {\n  required_providers {\n    aws = {\n      source  = "hashicorp/aws"\n      version = "~> 5.0"\n    }\n  }\n}` },
          { label: 'Initialize:', lang: 'bash', content: `terraform init` },
        ],
      },
      {
        title: 'Use a Module',
        codes: [
          { label: 'main.tf:', lang: 'hcl', content: `module "vpc" {\n  source  = "${base.replace(/^https?:\/\//, '')}/myorg/vpc/aws"\n  version = "1.0.0"\n}` },
          { label: 'Initialize:', lang: 'bash', content: `terraform init` },
        ],
      },
      {
        title: 'Publish a Provider',
        codes: [{ label: 'Upload binary for linux_amd64:', lang: 'bash', content: `curl -u admin:admin123 \\\n  -X PUT \\\n  --data-binary @terraform-provider-myprovider_1.0.0_linux_amd64.zip \\\n  "${base}/repository/terraform-hosted/v1/providers/myorg/myprovider/1.0.0/upload/linux/amd64"` }],
      },
      {
        title: 'Publish a Module',
        codes: [{ label: 'Upload module archive:', lang: 'bash', content: `tar -czf mymodule-1.0.0.tar.gz -C mymodule/ .\ncurl -u admin:admin123 \\\n  -X PUT \\\n  --data-binary @mymodule-1.0.0.tar.gz \\\n  "${base}/repository/terraform-hosted/v1/modules/myorg/mymodule/aws/1.0.0"` }],
      },
    ],
  },
  {
    id: 'alpine',
    name: 'Alpine (apk)',
    icon: '🏔',
    iconUrl: 'https://cdn.simpleicons.org/alpinelinux/0D597F',
    description: 'Alpine Linux apk repository. Serves .apk packages with an auto-generated APKINDEX.tar.gz index. Unsigned — clients need --allow-untrusted.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/alpine-hosted/` }],
      },
      {
        title: 'Configure /etc/apk/repositories',
        codes: [{ lang: 'bash', content: `echo "${base}/repository/alpine-hosted" \\\n  | sudo tee -a /etc/apk/repositories\n\nsudo apk update --allow-untrusted` }],
        note: 'This repository is unsigned — apk needs --allow-untrusted (or a per-instance policy that skips signature checks) until a signing key is configured.',
      },
      {
        title: 'Publish a .apk Package',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\\n  -H "Content-Type: application/octet-stream" \\\n  -T mypackage-1.0.0-r0.apk \\\n  "${base}/repository/alpine-hosted/x86_64/mypackage-1.0.0-r0.apk"` }],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using apk:', lang: 'bash', content: `sudo apk add --allow-untrusted mypackage` },
          { label: 'Direct .apk download and install:', lang: 'bash', content: `curl -u admin:admin123 \\\n  -O "${base}/repository/alpine-hosted/x86_64/mypackage-1.0.0-r0.apk"\nsudo apk add --allow-untrusted --no-network mypackage-1.0.0-r0.apk` },
        ],
      },
    ],
  },
]

function GuideRepositories() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Creating Repositories</h1>
        <p className={styles.sectionDesc}>
          Repositories are the core building blocks of Nexspence. Choose from Hosted (store your own artifacts), Proxy (cache a remote registry), or Group (combine multiple repos under one URL).
        </p>
      </div>
      <Step num={1} title="Open the Repositories page"
        text='Click "Repositories" in the sidebar. Then click the "+ New Repository" button in the top-right corner.'
        screenshot={{ src: '/docs/screenshots/repo-list-new-btn.png', alt: 'Repositories page with + New Repository button' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="Select the repository type"
        text="The wizard opens on the Type step. Choose one of three types:"
      />
      <div className={styles.typeCards}>
        <div className={styles.typeCard}>
          <div className={styles.typeCardName}>🗄 Hosted</div>
          <div className={styles.typeCardDesc}>Stores artifacts locally. Use for publishing your own packages.</div>
        </div>
        <div className={styles.typeCard} style={{ borderColor: 'rgba(34,211,238,0.2)', background: 'rgba(34,211,238,0.03)' }}>
          <div className={styles.typeCardName}>🔄 Proxy</div>
          <div className={styles.typeCardDesc}>Caches a remote registry (npmjs.com, PyPI, Docker Hub, etc.)</div>
        </div>
        <div className={styles.typeCard} style={{ borderColor: 'rgba(255,92,240,0.18)', background: 'rgba(255,92,240,0.03)' }}>
          <div className={styles.typeCardName}>🗂 Group</div>
          <div className={styles.typeCardDesc}>Merges several repos into one URL. Single endpoint for clients.</div>
        </div>
      </div>
      <Screenshot src="/docs/screenshots/create-repo-step1-type.png" alt="Wizard Step 1 — select Hosted, Proxy, or Group" />
      <hr className={styles.divider} />
      <Step num={3} title="Enter a name and select a format"
        text="On the Details step, enter a unique repository name (used in the URL) and select the format (Maven, npm, PyPI, Docker, etc.)."
        screenshot={{ src: '/docs/screenshots/create-repo-step2-details.png', alt: 'Wizard Step 2 — name and format fields' }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="(Proxy) Set the Remote URL"
        text="If you chose Proxy, enter the upstream registry URL on the Storage step. Common values:"
        code={{ lang: 'text', content: `Maven Central  → https://repo1.maven.org/maven2/\nnpm            → https://registry.npmjs.org/\nPyPI           → https://pypi.org/\nDocker Hub     → https://registry-1.docker.io/\nGo proxy       → https://proxy.golang.org/\nHelm stable    → https://charts.helm.sh/stable/\nCargo          → https://index.crates.io/` }}
      />
      <hr className={styles.divider} />
      <Step num={5} title="(Group) Add member repositories"
        text="If you chose Group, select member repositories on the Storage step. All members must share the same format. Order determines lookup priority — first match wins."
        note="A group cannot contain another group. Members must already exist."
        screenshot={{ src: '/docs/screenshots/create-repo-step3-group.png', alt: 'Wizard Step 3 — group member selection' }}
      />
      <hr className={styles.divider} />
      <Step num={6} title="Choose a blob store (optional)"
        text='The Storage step lets you pick which blob store holds the artifacts. Leave as "default" unless you have multiple blob stores configured (System Admin → Blob Stores).'
      />
      <hr className={styles.divider} />
      <Step num={7} title="Click Create and copy the URL"
        text="Click Create Repository. The new repo appears in the list. Click on it to see its URL — copy it to configure your build tool."
        screenshot={{ src: '/docs/screenshots/repo-detail-url.png', alt: 'Repository detail card with URL and copy button' }}
      />
    </>
  )
}
function GuideUsers() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Managing Users</h1>
        <p className={styles.sectionDesc}>
          Create local user accounts, assign roles, and manage API token access. Requires admin. Users can also be provisioned automatically via LDAP or OIDC/SAML SSO.
        </p>
      </div>
      <Step num={1} title="Open Security → Users"
        text='Click "Security" in the sidebar, then select the "Users" tab (visible to admins only).'
        screenshot={{ src: '/docs/screenshots/admin-users-tab.png', alt: 'System Admin page with Users tab selected' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="Create a new user"
        text='Click "+ Create User". Fill in username, first name, last name, email, and password. The username must be unique and is used for login and Basic Auth.'
        screenshot={{ src: '/docs/screenshots/create-user-form.png', alt: 'Create User form with all fields' }}
      />
      <hr className={styles.divider} />
      <Step num={3} title="Assign roles"
        text='After saving, click the shield icon (Assign Roles) in the user row. The transfer list lets you move roles from Available to Assigned. Click Save.'
        screenshot={{ src: '/docs/screenshots/assign-roles-dialog.png', alt: 'Assign Roles transfer list dialog' }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="View or revoke a user's API tokens"
        text="Each user manages their own API tokens from the profile modal (the key icon in the sidebar). Admins do not have a separate token revocation interface — each user is responsible for their own tokens."
        note="Token values are shown only once at creation. If a user loses a token, they must create a new one."
      />
    </>
  )
}
function GuideRolesPrivileges() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Roles & Privileges</h1>
        <p className={styles.sectionDesc}>
          Nexspence uses a three-layer RBAC model: Content Selector (what paths) → Privilege (permission scoped to a selector) → Role (bundle of privileges) → User (assigned roles).
        </p>
      </div>
      <Step num={1} title="Understand the model"
        text="Before creating anything, understand the chain: a Content Selector defines which artifact paths are in scope (via CEL expression). A Privilege links a Content Selector to a permission type. A Role bundles multiple privileges. A User is assigned one or more roles."
      />
      <hr className={styles.divider} />
      <Step num={2} title="Create a Content Selector first"
        text='Go to Security → Content Selectors → click "+ New". Write a CEL expression. Example — allow all Maven artifacts:'
        code={{ lang: 'cel', content: 'format == "maven2"' }}
        screenshot={{ src: '/docs/screenshots/content-selector-form.png', alt: 'Content Selector form with CEL expression input' }}
      />
      <hr className={styles.divider} />
      <Step num={3} title="Create a Privilege"
        text='Go to Security → Privileges → click "+ New". Select the Content Selector you just created. The privilege is automatically scoped to the paths matched by that selector.'
        screenshot={{ src: '/docs/screenshots/create-privilege-form.png', alt: 'Create Privilege form with Content Selector dropdown' }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="Create a Role"
        text='Go to Security → Roles → click "+ New Role". Give it a name (e.g. "maven-reader"), then add the privilege from Step 3.'
        screenshot={{ src: '/docs/screenshots/create-role-form.png', alt: 'Create Role form with privilege assignment list' }}
      />
      <hr className={styles.divider} />
      <Step num={5} title="Assign the Role to a User"
        text="Go to Security → Users tab. Click the Assign Roles button (shield icon) for the target user and add the new role. Changes take effect on the user's next API request."
        screenshot={{ src: '/docs/screenshots/assign-roles-dialog.png', alt: 'Assign Roles dialog with the new role selected' }}
      />
    </>
  )
}
function GuideContentSelectors() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Content Selectors</h1>
        <p className={styles.sectionDesc}>
          Content Selectors use CEL (Common Expression Language) to match artifact paths. They are the foundation of the privilege system — every privilege must reference a selector.
        </p>
      </div>
      <Step num={1} title="Open Content Selectors"
        text='Navigate to Security → Content Selectors. Click "+ New Content Selector".'
        screenshot={{ src: '/docs/screenshots/content-selectors-list.png', alt: 'Content Selectors list page with + New button' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="CEL expression fields"
        text="Expressions can reference these fields to match artifacts:"
        code={{ lang: 'text', content: `format      — repository format  ("maven2", "npm", "docker", "pypi", "helm", …)\npath        — artifact path      ("/com/example/myapp/1.0/myapp-1.0.jar")\nrepository  — repository name   ("maven-releases", "docker-hosted", …)` }}
      />
      <hr className={styles.divider} />
      <Step num={3} title="Example expressions"
        text="Copy any of these into the CEL expression field:"
        code={{ lang: 'cel', content: `# All artifacts (wildcard)\ntrue\n\n# All Maven artifacts\nformat == "maven2"\n\n# Specific Maven group only\nformat == "maven2" && path.startsWith("/com/mycompany/")\n\n# npm scoped packages only\nformat == "npm" && path.startsWith("/@myorg/")\n\n# Docker images in a specific repository\nformat == "docker" && repository == "docker-hosted"\n\n# Helm charts from any hosted repo\nformat == "helm" && repository.endsWith("-hosted")` }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="Save and use in a Privilege"
        text='Click Save. The selector appears in the Content Selector dropdown when creating a Privilege. See the Roles & Privileges guide for next steps.'
        screenshot={{ src: '/docs/screenshots/content-selector-saved.png', alt: 'Content Selectors list showing the newly created selector' }}
      />
    </>
  )
}
function GuideSecurityScanning() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Security Scanning</h1>
        <p className={styles.sectionDesc}>
          Nexspence scans artifacts for known CVE vulnerabilities using the OSV (Open Source Vulnerabilities) database at api.osv.dev. Supported formats: Maven, npm, PyPI, Cargo.
        </p>
      </div>
      <Step num={1} title="Open the Vulnerability Dashboard"
        text='Navigate to Security → Vulnerability Dashboard tab. The dashboard shows 6 severity cards and a paginated table of all findings across your repositories.'
        screenshot={{ src: '/docs/screenshots/vuln-dashboard.png', alt: 'Vulnerability Dashboard with severity cards (Critical, High, Medium, Low, Negligible, Unknown)' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="Run a bulk scan"
        text='Click "Rescan All" to queue a scan of all components in supported formats. Nexspence queries api.osv.dev for each package name + version. Results appear as the scan progresses.'
        note="Bulk scans call an external API (api.osv.dev). Ensure outbound HTTPS is allowed from your Nexspence host."
      />
      <hr className={styles.divider} />
      <Step num={3} title="Filter and inspect findings"
        text="Use the severity filter buttons to narrow the table. Each row shows the CVE ID, package, affected version, fix version (if available), and severity."
        screenshot={{ src: '/docs/screenshots/vuln-table-filter.png', alt: 'Vulnerability table with severity filter toolbar' }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="Scan a single component"
        text='From Search or Browse, open a component detail panel and click "Scan". Results are cached — click "Rescan" to force a fresh check against OSV.'
        screenshot={{ src: '/docs/screenshots/component-scan-result.png', alt: 'Component detail panel showing scan result with severity badges' }}
      />
      <hr className={styles.divider} />
      <Step num={5} title="Interpret severity levels"
        text="OSV maps each vulnerability to one of the following severity levels:"
        code={{ lang: 'text', content: `CRITICAL    — Actively exploitable. Immediate action required.\nHIGH        — Serious risk. Patch as soon as possible.\nMEDIUM      — Moderate risk. Patch in next release cycle.\nLOW         — Minor risk. Patch opportunistically.\nNEGLIGIBLE  — Theoretical risk. No known active exploits.\nUNKNOWN     — Severity not determined by OSV database.` }}
      />
    </>
  )
}
function GuideCleanupPolicies() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Cleanup Policies</h1>
        <p className={styles.sectionDesc}>
          Cleanup Policies delete old or unused artifacts automatically based on age, download inactivity, or version count. Policies run on a cron schedule or on demand.
        </p>
      </div>
      <Step num={1} title="Open Cleanup Policies"
        text='Click "Cleanup Policies" in the sidebar. Existing policies appear as cards showing their criteria and schedule.'
        screenshot={{ src: '/docs/screenshots/cleanup-policies-list.png', alt: 'Cleanup Policies page with policy cards' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="Create a new policy"
        text='Click "+ New Policy". The wizard has three steps: Identity (name + format filter), Criteria, and Schedule.'
        screenshot={{ src: '/docs/screenshots/cleanup-policy-wizard.png', alt: 'Create Policy wizard — Identification step' }}
      />
      <hr className={styles.divider} />
      <Step num={3} title="Set cleanup criteria"
        text="On the Criteria step, configure one or more rules. An artifact is a deletion candidate if it matches ANY enabled criterion (set to 0 to disable):"
        code={{ lang: 'text', content: `Last Downloaded  — delete if not downloaded in N days\nLast Modified    — delete if not updated in N days\nRetain N Versions — keep only the N newest versions per artifact name\n                    (older versions are deleted regardless of age)` }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="Set a schedule"
        text='On the Schedule step, enter a cron expression for automatic runs. Leave blank to run manually only.'
        code={{ lang: 'text', content: `0 2 * * *    — daily at 2:00 AM\n0 3 * * 0    — every Sunday at 3:00 AM\n0 0 1 * *    — first of every month at midnight` }}
      />
      <hr className={styles.divider} />
      <Step num={5} title="Attach the policy to a repository"
        text="On the Repositories page, click the gear icon on a repository card. Check your policy in the Cleanup Policy checklist and save. Multiple policies can be attached to one repository."
        screenshot={{ src: '/docs/screenshots/repo-attach-cleanup.png', alt: 'Repository settings panel with Cleanup Policy dropdown' }}
      />
      <hr className={styles.divider} />
      <Step num={6} title='Run manually with "Run"'
        text='On the Cleanup Policies page, click "Run" on any policy card to execute immediately. A summary shows how many artifacts were deleted.'
        screenshot={{ src: '/docs/screenshots/cleanup-run-now.png', alt: 'Policy card with Run Now button and deletion summary' }}
      />
    </>
  )
}
function GuideApiTokens() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>API Tokens</h1>
        <p className={styles.sectionDesc}>
          API tokens let you authenticate without using your password. Tokens start with <span className={styles.inlineCode}>nxs_</span> and work as a Basic Auth password or as a Bearer token header.
        </p>
      </div>
      <Step num={1} title="Open your profile"
        text="Click the key icon (🔑) at the bottom of the sidebar to open your profile modal. The modal shows your API tokens and a form to create new ones."
        screenshot={{ src: '/docs/screenshots/profile-api-tokens.png', alt: 'Profile modal with API Tokens tab open' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="Create a token"
        text='Click "+ Create Token". Enter a descriptive name (e.g. "ci-pipeline") and an optional expiry in days. The maximum allowed expiry is shown next to the input field.'
        screenshot={{ src: '/docs/screenshots/create-token-form.png', alt: 'Create Token dialog with name and expiry fields' }}
      />
      <hr className={styles.divider} />
      <Step num={3} title="Copy the token immediately"
        text="After creation, the full token is displayed once. Click the Copy button — it will not be shown again. Store it in a secrets manager or CI secrets vault."
        note="If you lose the token value, delete it and create a new one. There is no way to retrieve the value after closing this dialog."
        screenshot={{ src: '/docs/screenshots/token-created-copy.png', alt: 'Newly created token value with copy button highlighted' }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="Use the token as Basic Auth password"
        text="Pass the token as the HTTP Basic Auth password. Your username stays the same:"
        code={{ lang: 'bash', content: `# curl\ncurl -u admin:nxs_your_token_here \\\n  "https://nexspence.example.com/service/rest/v1/repositories"\n\n# Maven ~/.m2/settings.xml\n<password>nxs_your_token_here</password>\n\n# npm ~/.npmrc\n//nexspence.example.com/repository/npm-hosted/:_authToken=nxs_your_token_here` }}
      />
      <hr className={styles.divider} />
      <Step num={5} title="Use the token as a Bearer header"
        text="Alternatively, pass the token as an Authorization: Bearer header (no username needed):"
        code={{ lang: 'bash', content: `curl -H "Authorization: Bearer nxs_your_token_here" \\\n  "https://nexspence.example.com/service/rest/v1/repositories"` }}
      />
      <hr className={styles.divider} />
      <Step num={6} title="Revoke a token"
        text="On the API Tokens tab, click the ✕ button next to any token to revoke it immediately. Revoked tokens are rejected on the next API call."
        screenshot={{ src: '/docs/screenshots/token-revoke.png', alt: 'API Tokens list with revoke (X) button' }}
      />
    </>
  )
}

function GettingStarted({ base }: { base: string }) {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Getting Started</h1>
        <p className={styles.sectionDesc}>
          Learn how to authenticate and connect your build tools to Nexspence.
        </p>
      </div>

      <p className={styles.blockTitle}>Your Base URL</p>
      <UrlBlock url={base} />

      <p className={styles.blockTitle}>Authentication</p>
      <p className={styles.blockText}>Three methods are supported — use any one:</p>
      <CodeBlock lang="bash" content={`# 1. Username + password
curl -u admin:admin123 "${base}/api/v1/repositories"

# 2. API token as password (get from Profile → API Tokens)
curl -u admin:nxs_your_token_here "${base}/api/v1/repositories"

# 3. Bearer token
curl -H "Authorization: Bearer nxs_your_token_here" \\
  "${base}/api/v1/repositories"`} />

      <p className={styles.blockTitle}>Generate an API Token</p>
      <p className={styles.blockText}>
        Click the key icon in the sidebar → <strong style={{ color: 'var(--holo-text)' }}>API Tokens</strong> → Create Token.
        Tokens start with <span className={styles.inlineCode}>nxs_</span> and work as a password in Basic Auth
        or as a Bearer token header.
      </p>

      <p className={styles.blockTitle}>List Repositories</p>
      <CodeBlock lang="bash" content={`curl -u admin:admin123 \\
  "${base}/service/rest/v1/repositories" | python3 -m json.tool`} />

      <p className={styles.blockTitle}>Nexus API Compatibility</p>
      <p className={styles.blockText}>
        Nexspence is a drop-in replacement for Sonatype Nexus OSS.
        All Nexus REST API endpoints under <span className={styles.inlineCode}>/service/rest/v1/</span> are supported.
        Tools already configured for Nexus work without modification.
      </p>

      <p className={styles.blockTitle}>Browse & Search</p>
      <p className={styles.blockText}>
        Use the <strong style={{ color: 'var(--holo-text)' }}>Browse</strong> and <strong style={{ color: 'var(--holo-text)' }}>Search</strong> pages
        in the sidebar to explore artifacts visually, or use the Nexus REST API:
      </p>
      <CodeBlock lang="bash" content={`# Search by name
curl -u admin:admin123 \\
  "${base}/service/rest/v1/search?name=myapp" | python3 -m json.tool

# List assets in a repository
curl -u admin:admin123 \\
  "${base}/service/rest/v1/components?repository=maven-releases"`} />
    </>
  )
}

function FormatContent({ format, base }: { format: Format; base: string }) {
  const sections = format.sections(base)
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>{format.icon} {format.name}</h1>
        <p className={styles.sectionDesc}>{format.description}</p>
      </div>
      {sections.map((s, i) => (
        <div key={i}>
          <SectionBlock section={s} />
          {i < sections.length - 1 && <hr className={styles.divider} />}
        </div>
      ))}
    </>
  )
}

export default function DocsPage() {
  const [active, setActive] = useState('getting-started')
  const base = window.location.origin

  return (
    <div className={styles.docsLayout}>
      <nav className={styles.docsNav}>
        <button
          className={`${styles.docsNavBtn} ${active === 'getting-started' ? styles.active : ''}`}
          onClick={() => setActive('getting-started')}
        >
          <BookOpen size={14} style={{ flexShrink: 0 }} />
          Getting Started
        </button>

        <div className={styles.docsNavSection}>Guides</div>
        {([
          { id: 'guide-repos',     label: '🗄 Creating Repositories' },
          { id: 'guide-users',     label: '👥 Managing Users' },
          { id: 'guide-roles',     label: '🛡 Roles & Privileges' },
          { id: 'guide-selectors', label: '🔍 Content Selectors' },
          { id: 'guide-security',  label: '🔐 Security Scanning' },
          { id: 'guide-cleanup',   label: '🗑 Cleanup Policies' },
          { id: 'guide-tokens',    label: '🔑 API Tokens' },
        ] as { id: string; label: string }[]).map(g => (
          <button
            key={g.id}
            className={`${styles.docsNavBtn} ${active === g.id ? styles.active : ''}`}
            onClick={() => setActive(g.id)}
          >
            {g.label}
          </button>
        ))}

        <div className={styles.docsNavSection}>Formats</div>
        {FORMATS.map(f => (
          <button
            key={f.id}
            className={`${styles.docsNavBtn} ${active === f.id ? styles.active : ''}`}
            onClick={() => setActive(f.id)}
          >
            {f.iconUrl
              ? <img src={f.iconUrl} alt="" width={14} height={14} className={styles.navBrandIcon} onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = 'none' }} />
              : <span style={{ fontSize: 14, lineHeight: 1, flexShrink: 0 }}>{f.icon}</span>
            }
            {f.name}
          </button>
        ))}
      </nav>

      <div className={styles.docsContent}>
        {active === 'getting-started' && <GettingStarted base={base} />}
        {active === 'guide-repos'     && <GuideRepositories />}
        {active === 'guide-users'     && <GuideUsers />}
        {active === 'guide-roles'     && <GuideRolesPrivileges />}
        {active === 'guide-selectors' && <GuideContentSelectors />}
        {active === 'guide-security'  && <GuideSecurityScanning />}
        {active === 'guide-cleanup'   && <GuideCleanupPolicies />}
        {active === 'guide-tokens'    && <GuideApiTokens />}
        {FORMATS.map(f => active === f.id && <FormatContent key={f.id} format={f} base={base} />)}
      </div>
    </div>
  )
}
