// コミットすると秘密の情報が漏れそうなファイル名。ファイル名（ディレクトリを除いた部分）で判断する
const SECRET_NAMES = [
    // 環境変数
    /^\.env(\..+)?$/,
    // 鍵・証明書
    /\.(pem|key|p12|pfx|keystore|jks)$/,
    /^id_(rsa|dsa|ecdsa|ed25519)$/,
    // 認証情報
    /^credentials(\.json)?$/,
    /^service-account.*\.json$/,
    /^\.(npmrc|pypirc|netrc|htpasswd)$/,
    /^secrets?\.(json|ya?ml|toml|env|txt)$/,
    // インフラの状態や変数（パスワードなどが入りやすい）
    /\.tfstate(\.backup)?$/,
    /\.tfvars$/,
    /^kubeconfig$/,
];

// .env.example のような見本はコミットするものなので外す
const SAMPLE = /\.(example|sample|template|dist)$/;

export function isSecretFile(path: string) {
    const name = path.split('/').pop() ?? path;
    return !SAMPLE.test(name) && SECRET_NAMES.some((pattern) => pattern.test(name));
}
