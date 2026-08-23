// Japanese Help page; mirrors Help.en section for section, anchor ids
// included. UI labels quoted here must match the rendered app (the ja
// catalog); when a label changes, this copy changes with it.
// Deployment facts (which providers run) live on the About page, not
// here.
export default function HelpJa() {
  return (
    <main aria-label="ヘルプ" className="mx-auto w-full max-w-2xl p-6">
      <h2 className="mb-4 text-2xl font-bold">ヘルプ</h2>
      <div className="flex flex-col gap-6 text-sm text-gray-700">
        <section aria-labelledby="shelves-from-tags">
          <h3 id="shelves-from-tags" className="mb-1 font-semibold text-gray-900">
            タグで作る棚
          </h3>
          <p>
            棚はコレクションのビューを保存したものなので、ひとつのフィルターに一致するアイテムだけでなく、自分で選んだアイテムを自由にまとめられます。コツはタグにあります。
          </p>
          <ol className="mt-2 flex list-decimal flex-col gap-1 pl-5">
            <li>
              コレクションページのアイテムタブでテーブル表示に切り替え、「一括編集」を押します。
            </li>
            <li>
              まとめたいアイテムにチェックを入れ、表示されるバーの「タグを追加」で新しいタグ（例：trade-pile）を付けます。
            </li>
            <li>
              フィルターを開き、「タグ（すべて含む）」でそのタグにチェックを入れます。これで一覧には選んだアイテムだけが表示されます。
            </li>
            <li>
              棚の選択メニューで「棚として保存...」を押し、名前を付けます。棚はタグのフィルターを保持するので、後から別のアイテムに同じタグを付けると、そのアイテムも棚に追加されます。
            </li>
          </ol>
        </section>
        <section aria-labelledby="visibility">
          <h3 id="visibility" className="mb-1 font-semibold text-gray-900">
            棚の公開範囲
          </h3>
          <p>
            棚とプロフィールには、それぞれ3つの設定のいずれかがあります。非公開（自分だけ）、限定公開（リンクを知っているログイン済みのユーザー）、公開（「見つける」と検索に表示される）です。棚が見えるかどうかは、両方の設定で決まります。プロフィールが非公開なら、公開に設定した棚も他の人には見えません。プロフィールが限定公開なら、公開に設定した棚もリンクから開けますが、「見つける」には載りません。設定がこのように組み合わさっているときは、棚タブに通知が表示されます。
          </p>
        </section>
        <section aria-labelledby="currencies">
          <h3 id="currencies" className="mb-1 font-semibold text-gray-900">
            通貨の表示
          </h3>
          <p>
            ヘッダーの通貨セレクターは、相場の表示に使う通貨を切り替えます。相場はUSDで保存され、欧州中央銀行の参考レートで換算されます。適用したレートの日付は価格パネルに表示されます。購入価格は別です。入力した通貨のまま保持され、換算されることはありません。
          </p>
        </section>
        <section aria-labelledby="adding">
          <h3 id="adding" className="mb-1 font-semibold text-gray-900">
            ゲームとハードウェアの追加
          </h3>
          <p>
            「追加」では、まず共有カタログを検索します。ゲームかハードウェアを選び、検索して、結果をひとつ選びます。詳細ステップでは、リージョン、パッケージ、状態、購入価格といった手元の品の情報を記録します。ゲームの場合、自動の価格リスティング照合が誤ったリスティングを選んだときのために「手動で照合」も用意されています。カタログにないものは、「見つかりませんか？カスタムアイテムとして追加できます。」からカスタムアイテムとして作成でき、そのアイテムページの「カタログに申請」で共有カタログへの追加を提案できます。
          </p>
        </section>
        <section aria-labelledby="native-titles">
          <h3 id="native-titles" className="mb-1 font-semibold text-gray-900">
            ハードウェアを日本語タイトルで登録する
          </h3>
          <p>
            ハードウェア検索は価格カタログのローマ字名しか認識しません。「super famicom」なら見つかりますが、「スーパーファミコン」では見つかりません。日本語タイトルで棚に置きたいときは、カスタムアイテムとして追加します。「追加」の検索画面で「見つかりませんか？カスタムアイテムとして追加できます。」を押し、名前に日本語タイトルを入力します。次に作成したアイテムを開き、価格設定を「代用（別のリスティングの価格を使用）」に切り替えて、「価格ソースを選択」でその機器のリスティングをローマ字（例：「super famicom console」）で検索して選びます。アイテムは日本語タイトルのまま、そのリスティングの市場価格を追跡します。
          </p>
        </section>
        <section aria-labelledby="prices">
          <h3 id="prices" className="mb-1 font-semibold text-gray-900">
            相場の出どころ
          </h3>
          <p>
            アイテムの相場は、価格リスティングとの照合で決まります。確認ステップに照合結果が表示され、そこで変更もできます。相場は、設定したパッケージ（箱説なし・完品・未開封）に従います。カスタムアイテムには最初は相場が付かず、よく似た掲載アイテムを価格ソースとして選ぶと表示されるようになります。このインスタンスが使用している価格と為替レートのプロバイダーは、「このサイトについて」ページに記載されています。
          </p>
        </section>
      </div>
    </main>
  )
}
