import { definePrint, PrintDocument } from "../../src/print"

type Invoice = { no: string; items: { name: string; amount: number }[] }

/** 测试用的打印模板。 */
export const templates = {
  invoice: definePrint<Invoice>(({ data }) => (
    <PrintDocument title={`发票 ${data.no}`} styles={["h1 { color: rgb(1, 2, 3); }"]}>
      <h1>发票 {data.no}</h1>
      <table>
        <tbody>
          {data.items.map((it) => (
            <tr key={it.name}>
              <td>{it.name}</td>
              <td>{it.amount}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </PrintDocument>
  )),
}
