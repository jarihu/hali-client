package main

import (
	"fmt"
	"github.com/anacrolix/torrent/metainfo"
	"os"
)

func main() {
	p := `C:\ProgramData\Hali\torrents\bfa01f31ba1cb1f0382deade292bc222a6efd34d.torrent`
	mi, err := metainfo.LoadFromFile(p)
	if err != nil {
		panic(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		panic(err)
	}
	fmt.Println("CreatedBy:", mi.CreatedBy)
	fmt.Println("HasV1:", info.HasV1(), "HasV2:", info.HasV2())
	fmt.Println("MetaVersion:", info.MetaVersion)
	fmt.Println("Name:", info.Name, "Length:", info.Length, "PieceLength:", info.PieceLength)
	fmt.Println("Pieces bytes:", len(info.Pieces))
	fmt.Println("PieceLayers entries:", len(mi.PieceLayers))
	m2, err := mi.MagnetV2()
	if err != nil {
		panic(err)
	}
	fmt.Println("MagnetV2:", m2.String())

	fmt.Println("FileTree root is dir:", info.FileTree.IsDir(), "entries:", info.FileTree.NumEntries())
	if info.FileTree.IsDir() {
		fmt.Println("Root keys:")
		for k := range info.FileTree.Dir {
			fmt.Printf("  %q\n", k)
		}
	} else {
		fmt.Println("Root file length:", info.FileTree.File.Length, "piecesRootLen:", len(info.FileTree.File.PiecesRoot))
	}

	// Dump raw info bytes length for sanity.
	fmt.Println("InfoBytes len:", len(mi.InfoBytes))
	_ = os.Stdout.Sync()
}
