import { site } from '../../lib/site'

// Japanese Terms page; mirrors Terms.en section for section.
// Boilerplate terms for a self-hosted instance, not legal advice.
// Operators deploying publicly should have this reviewed. Statements
// about deletion and content must track actual app behavior; change
// the Last updated date when the text changes.
export default function TermsJa() {
  const s = site()
  const providers =
    s.authProviders.map((p) => p.label).join('または') || '第三者のログインプロバイダー'
  const contact = s.contact || 'このインスタンスの運営者'
  return (
    <main aria-label="利用規約" className="mx-auto w-full max-w-2xl p-6">
      <h2 className="mb-4 text-2xl font-bold">利用規約</h2>
      <div className="flex flex-col gap-4 text-sm text-gray-700">
        <section aria-label="同意">
          <h3 className="mb-1 font-semibold text-gray-900">同意</h3>
          <p>
            本規約は、{s.name}
            の利用に適用されます。アカウントを作成するか本サイトを利用することで、利用者は本規約に同意したものとみなされます。
          </p>
        </section>
        <section aria-label="アカウント">
          <h3 className="mb-1 font-semibold text-gray-900">アカウント</h3>
          <p>
            ログインには{providers}
            のアカウントが必要です。自分のアカウントのもとで行われたことについては利用者が責任を負います。アカウントは、アカウントページからいつでも削除できます。
          </p>
        </section>
        <section aria-label="利用者のコンテンツ">
          <h3 className="mb-1 font-semibold text-gray-900">利用者のコンテンツ</h3>
          <p>
            入力した内容、すなわちコレクションのアイテム、タグ、棚、カタログ申請、コメント、プロフィールの各項目は、引き続き利用者のものです。利用者は運営者に対し、本サイトの機能に必要な範囲でその内容を保存および表示する権利を許諾します。たとえば、コメントが残された棚を見ている人にそのコメントを表示することがこれにあたります。
          </p>
          <p className="mt-2">
            アカウントを削除すると、コレクション、タグ、棚、連携済みログイン、プロフィールが削除されます。他の人の棚に残したコメントは本文と投稿者名が失われ、「退会したユーザー」という表示だけが残ります。共有カタログに承認された投稿はカタログに残ります。これらにはアカウントへのリンクは保存されていません。
          </p>
        </section>
        <section aria-label="禁止事項">
          <h3 className="mb-1 font-semibold text-gray-900">禁止事項</h3>
          <p>
            {s.name}
            を、他者への嫌がらせ、違法なコンテンツの投稿、サービスの妨害、大量のスクレイピングに使わないでください。運営者は、該当するコンテンツを削除し、または該当するアカウントを閉鎖することがあります。
          </p>
        </section>
        <section aria-label="利用の終了">
          <h3 className="mb-1 font-semibold text-gray-900">利用の終了</h3>
          <p>
            {s.name}
            の利用はいつでもやめることができ、アカウントはアカウントページから削除できます。運営者は、本規約に違反したアカウントを一時停止または閉鎖することがあります。また、サービスの運営自体を終了することもあります。本ソフトウェアはセルフホスト型であり、インスタンスは運営者が運営を続けている間だけ存続します。
          </p>
        </section>
        <section aria-label="無保証と責任の制限">
          <h3 className="mb-1 font-semibold text-gray-900">無保証と責任の制限</h3>
          <p>
            {s.name}
            は現状のまま提供され、いかなる種類の保証もありません。相場と為替レートは第三者のデータから作られた推定値であり、誤っていたり古くなっていたりすることがあります。法律の許す範囲において、運営者は、データの損失や表示された価格に基づく判断を含め、本サイトの利用から生じる損害について責任を負いません。
          </p>
        </section>
        <section aria-label="変更">
          <h3 className="mb-1 font-semibold text-gray-900">変更</h3>
          <p>
            運営者は本規約を変更することがあります。変更があった場合は下記の日付が更新されます。変更後も利用を続けた場合、変更に同意したものとみなされます。
          </p>
        </section>
        <section aria-label="準拠法">
          <h3 className="mb-1 font-semibold text-gray-900">準拠法</h3>
          <p>本規約は、{s.jurisdiction || '運営者の所在地'}の法に準拠します。</p>
        </section>
        <section aria-label="お問い合わせ">
          <h3 className="mb-1 font-semibold text-gray-900">お問い合わせ</h3>
          <p>本規約に関するお問い合わせは、{contact}までお願いします。</p>
        </section>
        <p className="text-xs text-gray-500">最終更新日：2026-07-30</p>
      </div>
    </main>
  )
}
