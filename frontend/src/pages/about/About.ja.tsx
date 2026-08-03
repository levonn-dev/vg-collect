import { i18n } from '@lingui/core'
import { site } from '../../lib/site'

// Japanese About page; mirrors About.en slot for slot, and a
// structural change there lands here in the same change. Deployment
// facts (operator, contact, active sources) come from site();
// everything else describes the software and holds for every
// instance.
const SOURCE_NOTES: Record<string, string> = {
  igdb: 'タイトル、プラットフォーム、ジャンル、発売日、カバーアートはIGDBのカタログに由来します。カバー画像はIGDBから直接読み込まれます。',
  pricecharting:
    '箱説なし・完品・未開封それぞれの相場は、PriceChartingのリスティングに由来します。',
  frankfurter:
    '通貨の換算には、frankfurter.devが公開する欧州中央銀行の参考レートを使用します。',
}

export default function AboutJa() {
  const s = site()
  return (
    <main aria-label="このサイトについて" className="mx-auto w-full max-w-2xl p-6">
      <h2 className="mb-4 text-2xl font-bold">{s.name}について</h2>
      <div className="flex flex-col gap-4 text-sm text-gray-700">
        <p>
          {s.name}
          は、ビデオゲームのコレクションを管理するセルフホスト型ソフトウェアvgkeepのインスタンスです。所有しているゲームやハードウェアを登録し、タグと棚で整理し、相場を追い、このインスタンスの他のユーザーと棚を共有できます。
        </p>
        <p>
          このインスタンスは、{s.operator || 'このインスタンスの運営者'}が運営しています。
          {s.contact && (
            <>
              連絡先：
              <a className="underline hover:text-gray-900" href={`mailto:${s.contact}`}>
                {s.contact}
              </a>
              。
            </>
          )}
        </p>
        <section aria-label="ソースコード">
          <h3 className="mb-1 font-semibold text-gray-900">ソースコード</h3>
          <p>
            vgkeepは、AGPL-3.0ライセンスのもとで提供される自由ソフトウェアです。このインスタンスのソースコードは
            <a className="underline hover:text-gray-900" href={s.sourceUrl}>
              {s.sourceUrl}
            </a>
            で入手できます。
          </p>
        </section>
        {s.dataSources.length > 0 && (
          <section aria-label="データソース">
            <h3 className="mb-1 font-semibold text-gray-900">データソース</h3>
            <ul className="flex flex-col gap-2">
              {s.dataSources.map((d) => (
                <li key={d.key}>
                  {i18n._(d.dataType)}は
                  <a className="underline hover:text-gray-900" href={d.url}>
                    {d.label}
                  </a>
                  から提供されています。{SOURCE_NOTES[d.key]}
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    </main>
  )
}
