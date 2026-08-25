import { site } from '../../lib/site'

// Mirrors Privacy.en section for section. Must track actual app
// behavior; not legal advice. Move the Last updated date on change.
export default function PrivacyJa() {
  const s = site()
  const providers =
    s.authProviders.map((p) => p.label).join('または') || 'ご利用のログインプロバイダー'
  const igdbActive = s.dataSources.some((d) => d.key === 'igdb')
  const contact = s.contact || 'このインスタンスの運営者'
  return (
    <main id="main-content" tabIndex={-1} aria-label="プライバシーポリシー" className="mx-auto w-full max-w-2xl p-6">
      <h2 className="mb-4 text-2xl font-bold">プライバシーポリシー</h2>
      <div className="flex flex-col gap-4 text-sm text-gray-700">
        <section aria-label="保存される情報">
          <h3 className="mb-1 font-semibold text-gray-900">保存される情報</h3>
          <p>
            アカウント：{providers}でログインすると、{s.name}
            はメールアドレス、表示名、アバター画像へのリンクを保存します。アバター画像そのものは、ログインプロバイダーのサーバーから読み込まれます。
          </p>
          <p className="mt-2">
            本サイトを通じて入力したすべての内容：コレクションのアイテム、タグ、棚、カタログ申請、コメント、フォロー、いいね、プロフィール設定。
          </p>
        </section>
        <section aria-label="Cookie">
          <h3 className="mb-1 font-semibold text-gray-900">Cookie</h3>
          <p>
            {s.name}
            が設定するCookieは、ログイン状態を保つ__Host-vg_sessionのひとつだけです。このCookieは暗号化されていてスクリプトからは読み取れず、セッションの終了とともに失効します。アクセス解析用、広告用、トラッキング用のCookieはありません。
          </p>
        </section>
        <section aria-label="第三者">
          <h3 className="mb-1 font-semibold text-gray-900">第三者</h3>
          {igdbActive && (
            <p>
              カバーアートは、ブラウザからIGDBのサーバーへ直接読み込まれます。IGDBのサーバーは、他の画像ホストと同じようにIPアドレスを受け取ります。
            </p>
          )}
          <p className={igdbActive ? 'mt-2' : undefined}>
            アバター画像も同じ仕組みで、ログインプロバイダーのサーバーから読み込まれます。価格と為替レートのデータは本サイトのサーバーが取得するため、これらのプロバイダーがブラウザからの通信を受け取ることはありません。
          </p>
        </section>
        <section aria-label="アカウントの削除">
          <h3 className="mb-1 font-semibold text-gray-900">アカウントの削除</h3>
          <p>
            アカウントページでアカウントを削除すると、コレクション、タグ、棚、連携済みログイン、プロフィールが削除されます。他の人の棚に残したコメントは本文と投稿者名が失われ、「退会したユーザー」という表示だけが残ります。共有カタログに承認された投稿は共有カタログに残りますが、アカウントへのリンクは保存されていません。
          </p>
        </section>
        <section aria-label="変更とお問い合わせ">
          <h3 className="mb-1 font-semibold text-gray-900">変更とお問い合わせ</h3>
          <p>
            運営者は本ポリシーを変更することがあります。変更があった場合は下記の日付が更新されます。データに関するお問い合わせは、{contact}
            までお願いします。
          </p>
        </section>
        <p className="text-xs text-gray-500">最終更新日：2026-07-30</p>
      </div>
    </main>
  )
}
